package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	pb "github.com/cami-fleet/control-plane/gen"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
	"github.com/cami-fleet/control-plane/internal/fleet"
	"github.com/cami-fleet/control-plane/internal/metrics"
	"github.com/cami-fleet/control-plane/internal/model"
	"github.com/cami-fleet/control-plane/internal/notify"
	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

type Server struct {
	pb.UnimplementedAgentServiceServer

	pg       *pgstore.Store
	ch       *chstore.Store
	nats     *natsclient.Client
	notifier *notify.WebhookNotifier

	mu sync.RWMutex
	// watchChs maps device_id -> channels of open WatchDeployments streams.
	// This is the only path a live deployment instruction takes to a device;
	// deployments created while a device is offline reach it on reconnect.
	watchChs map[string][]chan *pb.DeploymentInstruction
	// taskChs maps device_id -> channels of open WatchTasks streams.
	taskChs map[string][]chan *pb.AgentTask

	// deviceNames caches device_id -> registered name (= certificate CN).
	// A device's name never changes for a given id, so entries never go stale.
	deviceNames sync.Map
}

func NewServer(pg *pgstore.Store, ch *chstore.Store, nats *natsclient.Client) *Server {
	return &Server{
		pg:       pg,
		ch:       ch,
		nats:     nats,
		watchChs: make(map[string][]chan *pb.DeploymentInstruction),
		taskChs:  make(map[string][]chan *pb.AgentTask),
	}
}

// PushTask sends a task to the device's open WatchTasks streams and reports
// whether any stream took it. A task nobody took stays pending and is sent
// when the device next connects.
func (s *Server) PushTask(deviceID string, task *pb.AgentTask) bool {
	s.mu.RLock()
	chs := s.taskChs[deviceID]
	s.mu.RUnlock()
	delivered := false
	for _, ch := range chs {
		select {
		case ch <- task:
			delivered = true
		default:
		}
	}
	return delivered
}

// SetNotifier enables webhook alerts for failed deployments. A nil notifier
// disables them.
func (s *Server) SetNotifier(n *notify.WebhookNotifier) {
	s.notifier = n
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

// PushDeployment fans out a deployment instruction to all currently watching
// streams for a given device.
func (s *Server) PushDeployment(deviceID string, instr *pb.DeploymentInstruction) {
	s.mu.RLock()
	chs := s.watchChs[deviceID]
	s.mu.RUnlock()
	for _, ch := range chs {
		select {
		case ch <- instr:
		default:
			// The stream is not draining. The deployment stays pending in the
			// database and is re-sent when the device reconnects.
			log.Warn().Str("device_id", deviceID).Str("deployment_id", instr.DeploymentId).
				Msg("watch stream full; instruction will be re-sent on reconnect")
		}
	}
}

// ── Device identity ───────────────────────────────────────────────────────────
//
// mTLS proves a client holds a certificate signed by the fleet CA. Each device
// has its own certificate whose common name is the device name, so the name in
// the certificate — not the device_id in the request body — says who is
// calling. Every RPC checks that the device it acts on is the caller.

// peerName returns the common name of the caller's verified client certificate.
func peerName(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer information")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "connection is not mutual TLS")
	}
	chains := tlsInfo.State.VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "", status.Error(codes.Unauthenticated, "no verified client certificate")
	}
	cn := chains[0][0].Subject.CommonName
	if cn == "" {
		return "", status.Error(codes.Unauthenticated, "client certificate has no common name")
	}
	return cn, nil
}

// authorizeDevice returns nil only if deviceID belongs to the caller.
func (s *Server) authorizeDevice(ctx context.Context, rpc, deviceID string) error {
	caller, err := peerName(ctx)
	if err != nil {
		return err
	}
	name, err := s.deviceName(ctx, deviceID)
	if err != nil {
		metrics.IdentityRejections.WithLabelValues(rpc).Inc()
		return status.Error(codes.PermissionDenied, "unknown device")
	}
	if name != caller {
		metrics.IdentityRejections.WithLabelValues(rpc).Inc()
		log.Warn().Str("rpc", rpc).Str("cert_cn", caller).Str("claimed_device", name).
			Msg("rejected: device_id does not belong to the calling certificate")
		return status.Error(codes.PermissionDenied, "device_id does not match client certificate")
	}
	return nil
}

func (s *Server) deviceName(ctx context.Context, deviceID string) (string, error) {
	if v, ok := s.deviceNames.Load(deviceID); ok {
		return v.(string), nil
	}
	name, err := s.pg.DeviceName(ctx, deviceID)
	if err != nil {
		return "", err
	}
	s.deviceNames.Store(deviceID, name)
	return name, nil
}

// ── RPCs ─────────────────────────────────────────────────────────────────────

func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	caller, err := peerName(ctx)
	if err != nil {
		return nil, err
	}
	if req.DeviceName != caller {
		metrics.IdentityRejections.WithLabelValues("Register").Inc()
		log.Warn().Str("cert_cn", caller).Str("claimed_name", req.DeviceName).
			Msg("rejected: register name does not match client certificate")
		return nil, status.Error(codes.PermissionDenied, "device_name must match the client certificate's common name")
	}

	id, err := s.pg.UpsertDevice(ctx, req.DeviceName, req.Labels, req.AgentVersion)
	if err != nil {
		return nil, fmt.Errorf("register device: %w", err)
	}
	if err := s.pg.UpdateDeviceCapabilities(ctx, id, req.Arch, req.Os, int64(req.MemTotalMb), req.Accelerators); err != nil {
		log.Warn().Err(err).Str("device_id", id).Msg("store device capabilities failed")
	}
	s.deviceNames.Store(id, req.DeviceName)

	log.Info().Str("device", req.DeviceName).Str("id", id).Str("arch", req.Arch).Msg("device registered")
	s.nats.Publish(natsclient.SubjectDeviceOnline(id), map[string]string{"device_id": id, "name": req.DeviceName})
	return &pb.RegisterResponse{DeviceId: id}, nil
}

func (s *Server) SendHeartbeat(ctx context.Context, req *pb.Heartbeat) (*pb.HeartbeatAck, error) {
	if err := s.authorizeDevice(ctx, "SendHeartbeat", req.DeviceId); err != nil {
		return nil, err
	}
	if err := s.pg.TouchHeartbeat(ctx, req.DeviceId); err != nil {
		log.Warn().Err(err).Str("device_id", req.DeviceId).Msg("heartbeat update failed")
	}
	return &pb.HeartbeatAck{ServerTsUnix: time.Now().Unix()}, nil
}

func (s *Server) StreamTelemetry(stream pb.AgentService_StreamTelemetryServer) error {
	ctx := stream.Context()
	metrics.GRPCStreamsGauge.WithLabelValues("telemetry").Inc()
	defer metrics.GRPCStreamsGauge.WithLabelValues("telemetry").Dec()

	authorized := ""
	for {
		report, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.Empty{})
		}
		if err != nil {
			return err
		}
		if report.DeviceId != authorized {
			if err := s.authorizeDevice(ctx, "StreamTelemetry", report.DeviceId); err != nil {
				return err
			}
			authorized = report.DeviceId
		}
		row := &model.TelemetryRow{
			DeviceID: report.DeviceId,
			ModelID:  report.ModelId,
			TPS:      report.Tps,
			TTFTMs:   report.TtftMs,
			MemMB:    report.MemMb,
			Status:   report.Status,
			Source:   report.Source,
			Ts:       time.Unix(report.TsUnix, 0),
		}
		if err := s.ch.InsertTelemetry(ctx, row); err != nil {
			metrics.TelemetryInserts.WithLabelValues("error").Inc()
			log.Warn().Err(err).Msg("insert telemetry failed")
			continue
		}
		metrics.TelemetryInserts.WithLabelValues("ok").Inc()
	}
}

func (s *Server) WatchDeployments(req *pb.WatchRequest, stream pb.AgentService_WatchDeploymentsServer) error {
	deviceID := req.DeviceId
	ctx := stream.Context()
	if err := s.authorizeDevice(ctx, "WatchDeployments", deviceID); err != nil {
		return err
	}
	metrics.GRPCStreamsGauge.WithLabelValues("watch").Inc()
	defer metrics.GRPCStreamsGauge.WithLabelValues("watch").Dec()

	// Register before reading the database, so a deployment created between
	// the read and the registration is not missed. A deployment that shows up
	// in both is delivered twice; the agent ignores IDs it has already seen.
	ch := make(chan *pb.DeploymentInstruction, 8)
	s.mu.Lock()
	s.watchChs[deviceID] = append(s.watchChs[deviceID], ch)
	s.mu.Unlock()
	defer func() {
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

	// Desired state: make sure the newest deployment this device should run
	// applies to it. This is how a device that joined (or changed labels,
	// or upgraded) after a deployment was created still receives it.
	s.reconcile(ctx, deviceID)

	// Send deployments not yet finished on this device: never started, or
	// interrupted mid-download or mid-verify.
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

// reconcile targets the device with the deployment it should be running, if
// it is not targeted already. Errors are logged: a failed reconcile must not
// stop the device from receiving deployments it already has.
func (s *Server) reconcile(ctx context.Context, deviceID string) {
	dev, err := s.pg.GetDevice(ctx, deviceID)
	if err != nil {
		log.Warn().Err(err).Str("device_id", deviceID).Msg("reconcile: load device failed")
		return
	}
	candidates, err := s.pg.MatchingDeployments(ctx, deviceID)
	if err != nil {
		log.Warn().Err(err).Str("device_id", deviceID).Msg("reconcile: matching deployments failed")
		return
	}
	desired := fleet.Desired(candidates, deviceID)
	if desired == nil {
		return
	}
	// push=false: the pending query right after this sends it.
	outcome, reason, err := fleet.Target(ctx, s.pg, nil, desired, *dev, false)
	switch {
	case err != nil:
		log.Warn().Err(err).Str("device_id", deviceID).Msg("reconcile: target failed")
	case outcome == fleet.Targeted:
		log.Info().Str("device_id", deviceID).Str("deployment_id", desired.ID).Msg("reconcile: device now targeted")
		if err := s.pg.ReopenDeployment(ctx, desired.ID); err != nil {
			log.Warn().Err(err).Msg("reopen deployment failed")
		}
	case outcome == fleet.Skipped:
		log.Info().Str("device_id", deviceID).Str("deployment_id", desired.ID).Str("reason", reason).
			Msg("reconcile: device skipped")
	}
}

func (s *Server) AckDeployment(ctx context.Context, req *pb.DeploymentAck) (*pb.Empty, error) {
	if err := s.authorizeDevice(ctx, "AckDeployment", req.DeviceId); err != nil {
		return nil, err
	}
	if !model.IsAckStatus(req.Status) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid deployment status %q", req.Status)
	}
	targeted, err := s.pg.DeploymentHasDevice(ctx, req.DeploymentId, req.DeviceId)
	if err != nil {
		return nil, err
	}
	if !targeted {
		return nil, status.Error(codes.NotFound, "device is not a target of this deployment")
	}

	if err := s.pg.UpdateDeviceDeploymentStatus(ctx, req.DeploymentId, req.DeviceId, req.Status, req.ErrorMsg); err != nil {
		log.Warn().Err(err).Msg("update device deployment status failed")
		return nil, err
	}
	switch req.Status {
	case string(model.DDRunning):
		if dep, err := s.pg.GetDeployment(ctx, req.DeploymentId); err == nil {
			if err := s.pg.UpdateDeviceModel(ctx, req.DeviceId, dep.ModelID); err != nil {
				log.Warn().Err(err).Msg("update device model failed")
			}
		}
	case string(model.DDFailed):
		if s.notifier != nil {
			go s.notifier.NotifyDeploymentFailed(req.DeviceId, req.DeploymentId, req.ErrorMsg)
		}
	}
	if err := s.pg.CheckDeploymentCompletion(ctx, req.DeploymentId); err != nil {
		log.Warn().Err(err).Msg("check deployment completion failed")
	}
	return &pb.Empty{}, nil
}

// WatchTasks streams questions for the device's AI agent: first any that
// have no answer yet, then new ones as operators ask them.
func (s *Server) WatchTasks(req *pb.WatchRequest, stream pb.AgentService_WatchTasksServer) error {
	deviceID := req.DeviceId
	ctx := stream.Context()
	if err := s.authorizeDevice(ctx, "WatchTasks", deviceID); err != nil {
		return err
	}
	metrics.GRPCStreamsGauge.WithLabelValues("tasks").Inc()
	defer metrics.GRPCStreamsGauge.WithLabelValues("tasks").Dec()

	ch := make(chan *pb.AgentTask, 8)
	s.mu.Lock()
	s.taskChs[deviceID] = append(s.taskChs[deviceID], ch)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		chs := s.taskChs[deviceID]
		for i, c := range chs {
			if c == ch {
				s.taskChs[deviceID] = append(chs[:i], chs[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}()

	open, err := s.pg.OpenTasks(ctx, deviceID)
	if err != nil {
		log.Warn().Err(err).Str("device_id", deviceID).Msg("open tasks query failed")
	}
	for _, t := range open {
		if err := stream.Send(&pb.AgentTask{TaskId: t.ID, Prompt: t.Prompt, MaxSteps: uint32(t.MaxSteps)}); err != nil {
			return err
		}
		if err := s.pg.MarkTaskRunning(ctx, t.ID); err != nil {
			log.Warn().Err(err).Msg("mark task running failed")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case task := <-ch:
			if err := stream.Send(task); err != nil {
				return err
			}
		}
	}
}

// ReportTask stores the agent's answer. The task must belong to the calling
// device and must not already have a result.
func (s *Server) ReportTask(ctx context.Context, req *pb.TaskResult) (*pb.Empty, error) {
	if err := s.authorizeDevice(ctx, "ReportTask", req.DeviceId); err != nil {
		return nil, err
	}
	if !model.IsTaskResultStatus(req.Status) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid task status %q", req.Status)
	}
	err := s.pg.CompleteTask(ctx, req.TaskId, req.DeviceId, pgstore.TaskResult{
		Status:     model.TaskStatus(req.Status),
		Answer:     req.Answer,
		ErrorMsg:   req.ErrorMsg,
		Steps:      []byte(req.StepsJson),
		ModelID:    req.ModelId,
		DurationMs: int(req.DurationMs),
	})
	if errors.Is(err, pgstore.ErrTaskNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, err
	}
	if s.nats != nil {
		s.nats.Publish("task."+req.TaskId+".done", map[string]string{"task_id": req.TaskId, "device_id": req.DeviceId, "status": req.Status})
	}
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
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("no certificates found in %s/ca.crt", certDir)
	}

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
