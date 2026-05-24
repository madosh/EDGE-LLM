package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	pb "github.com/cami-fleet/control-plane/gen"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
	"github.com/cami-fleet/control-plane/internal/model"
	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

// deploymentMsg is JSON-encoded and published on NATS when a new deployment is created.
type deploymentMsg struct {
	DeploymentID   string `json:"deployment_id"`
	ModelID        string `json:"model_id"`
	ArtifactURL    string `json:"artifact_url"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

type Server struct {
	pb.UnimplementedAgentServiceServer

	pg   *pgstore.Store
	ch   *chstore.Store
	nats *natsclient.Client

	mu       sync.RWMutex
	// watchChs maps device_id -> channel of deployment instructions
	watchChs map[string][]chan *pb.DeploymentInstruction
}

func NewServer(pg *pgstore.Store, ch *chstore.Store, nats *natsclient.Client) *Server {
	return &Server{
		pg:       pg,
		ch:       ch,
		nats:     nats,
		watchChs: make(map[string][]chan *pb.DeploymentInstruction),
	}
}

// RegisterWatcher registers an in-memory channel for a device and returns it.
// Primarily used for testing; production code uses WatchDeployments gRPC stream.
func (s *Server) RegisterWatcher(deviceID string) chan *pb.DeploymentInstruction {
	ch := make(chan *pb.DeploymentInstruction, 8)
	s.mu.Lock()
	s.watchChs[deviceID] = append(s.watchChs[deviceID], ch)
	s.mu.Unlock()
	return ch
}

// PushDeployment fans out a deployment instruction to all currently watching streams
// for a given device, AND publishes on NATS (for future durability).
func (s *Server) PushDeployment(deviceID string, instr *pb.DeploymentInstruction) {
	s.mu.RLock()
	chs := s.watchChs[deviceID]
	s.mu.RUnlock()
	for _, ch := range chs {
		select {
		case ch <- instr:
		default:
		}
	}
}

func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	id, err := s.pg.UpsertDevice(ctx, req.DeviceName, req.Labels, req.AgentVersion)
	if err != nil {
		return nil, fmt.Errorf("register device: %w", err)
	}
	log.Info().Str("device", req.DeviceName).Str("id", id).Msg("device registered")
	s.nats.Publish(natsclient.SubjectDeviceOnline(id), map[string]string{"device_id": id, "name": req.DeviceName})
	return &pb.RegisterResponse{DeviceId: id}, nil
}

func (s *Server) SendHeartbeat(ctx context.Context, req *pb.Heartbeat) (*pb.HeartbeatAck, error) {
	if err := s.pg.TouchHeartbeat(ctx, req.DeviceId); err != nil {
		log.Warn().Err(err).Str("device_id", req.DeviceId).Msg("heartbeat update failed")
	}
	return &pb.HeartbeatAck{ServerTsUnix: time.Now().Unix()}, nil
}

func (s *Server) StreamTelemetry(stream pb.AgentService_StreamTelemetryServer) error {
	for {
		report, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.Empty{})
		}
		if err != nil {
			return err
		}
		row := &model.TelemetryRow{
			DeviceID: report.DeviceId,
			ModelID:  report.ModelId,
			TPS:      report.Tps,
			TTFTMs:   report.TtftMs,
			MemMB:    report.MemMb,
			Status:   report.Status,
			Ts:       time.Unix(report.TsUnix, 0),
		}
		if err := s.ch.InsertTelemetry(stream.Context(), row); err != nil {
			log.Warn().Err(err).Msg("insert telemetry failed")
		}
	}
}

func (s *Server) WatchDeployments(req *pb.WatchRequest, stream pb.AgentService_WatchDeploymentsServer) error {
	deviceID := req.DeviceId
	ctx := stream.Context()

	// Send any deployments already pending in the DB.
	pending, err := s.pg.GetPendingDeployments(ctx, deviceID)
	if err != nil {
		log.Warn().Err(err).Str("device_id", deviceID).Msg("get pending deployments failed")
	}
	for _, d := range pending {
		if err := stream.Send(&pb.DeploymentInstruction{
			DeploymentId:   d.ID,
			ModelId:        d.ModelID,
			ArtifactUrl:    d.ArtifactURL,
			ArtifactSha256: d.ArtifactSHA256,
		}); err != nil {
			return err
		}
	}

	// Register an in-memory channel so REST handler can push live instructions.
	ch := make(chan *pb.DeploymentInstruction, 8)
	s.mu.Lock()
	s.watchChs[deviceID] = append(s.watchChs[deviceID], ch)
	s.mu.Unlock()

	// Also subscribe to NATS so control-plane instances can cross-notify.
	sub, err := s.nats.Subscribe(natsclient.SubjectDeployment(deviceID), func(data []byte) {
		var msg deploymentMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		select {
		case ch <- &pb.DeploymentInstruction{
			DeploymentId:   msg.DeploymentID,
			ModelId:        msg.ModelID,
			ArtifactUrl:    msg.ArtifactURL,
			ArtifactSha256: msg.ArtifactSHA256,
		}:
		default:
		}
	})
	if err != nil {
		log.Warn().Err(err).Str("device_id", deviceID).Msg("nats subscribe failed")
	}

	defer func() {
		if sub != nil {
			sub.Unsubscribe()
		}
		s.mu.Lock()
		chs := s.watchChs[deviceID]
		for i, c := range chs {
			if c == ch {
				s.watchChs[deviceID] = append(chs[:i], chs[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}()

	log.Info().Str("device_id", deviceID).Msg("watching deployments")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case instr := <-ch:
			if err := stream.Send(instr); err != nil {
				return err
			}
		}
	}
}

func (s *Server) AckDeployment(ctx context.Context, req *pb.DeploymentAck) (*pb.Empty, error) {
	if err := s.pg.UpdateDeviceDeploymentStatus(ctx, req.DeploymentId, req.DeviceId, req.Status, req.ErrorMsg); err != nil {
		log.Warn().Err(err).Msg("update device deployment status failed")
		return nil, err
	}
	if req.Status == "running" {
		// Fetch the deployment to get model_id
		dep, err := s.pg.GetDeployment(ctx, req.DeploymentId)
		if err == nil {
			s.pg.UpdateDeviceModel(ctx, req.DeviceId, dep.ModelID)
		}
	}
	s.pg.CheckDeploymentCompletion(ctx, req.DeploymentId)
	return &pb.Empty{}, nil
}

// GRPCHandle wraps a gRPC server and its listener for lifecycle management.
type GRPCHandle struct {
	server   *ggrpc.Server
	listener net.Listener
}

// Serve starts accepting connections. Blocks until stopped.
func (h *GRPCHandle) Serve() error {
	return h.server.Serve(h.listener)
}

// GracefulStop drains active RPCs before stopping.
func (h *GRPCHandle) GracefulStop() {
	h.server.GracefulStop()
}

// Listen creates and binds the gRPC listener with mTLS. Call Serve() to start accepting.
func Listen(addr, certDir string, srv *Server) (*GRPCHandle, error) {
	serverCert, err := tls.LoadX509KeyPair(
		certDir+"/server.crt",
		certDir+"/server.key",
	)
	if err != nil {
		return nil, fmt.Errorf("load server cert: %w", err)
	}
	caCert, err := os.ReadFile(certDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS13,
	}

	grpcSrv := ggrpc.NewServer(
		ggrpc.Creds(credentials.NewTLS(tlsCfg)),
		ggrpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 60 * time.Second,
			Time:              30 * time.Second,
			Timeout:           10 * time.Second,
		}),
	)
	pb.RegisterAgentServiceServer(grpcSrv, srv)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Info().Str("addr", addr).Msg("gRPC server listening (mTLS)")
	return &GRPCHandle{server: grpcSrv, listener: lis}, nil
}
