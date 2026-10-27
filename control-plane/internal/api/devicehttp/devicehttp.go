// Package devicehttp serves the device API as JSON over HTTPS, for devices
// too small to speak gRPC over HTTP/2 (an ESP32 status display, for example).
//
// It is not a second trust model. The listener requires a client certificate
// signed by the fleet CA, the caller is that certificate's common name, and
// every call goes through the same checks as the gRPC AgentService: a device
// can register only under its own name and act only on its own device ID.
// What it leaves out is streaming: an HTTP device polls /state instead of
// holding WatchDeployments open.
package devicehttp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/cami-fleet/control-plane/gen"
	"github.com/cami-fleet/control-plane/internal/model"
	"github.com/cami-fleet/control-plane/internal/mtls"
)

// assignmentsLimit is how many of its newest deployments a device sees.
const assignmentsLimit = 5

// Devices is the device-facing logic of the gRPC server, called with the
// caller taken from this listener's TLS state.
type Devices interface {
	RegisterAs(ctx context.Context, caller string, req *pb.RegisterRequest) (*pb.RegisterResponse, error)
	HeartbeatAs(ctx context.Context, caller string, req *pb.Heartbeat) (*pb.HeartbeatAck, error)
	AckAs(ctx context.Context, caller string, req *pb.DeploymentAck) (*pb.Empty, error)
	Authorize(ctx context.Context, rpc, caller, deviceID string) error
	Reconcile(ctx context.Context, deviceID string)
}

// Store is the slice of the Postgres store the state endpoint reads.
type Store interface {
	GetDevice(ctx context.Context, id string) (*model.Device, error)
	DeviceAssignments(ctx context.Context, deviceID string, limit int) ([]model.Assignment, error)
	CountDevicesByStatus(ctx context.Context) (map[string]int, error)
	CountDeploymentsByStatus(ctx context.Context) (map[string]int, error)
}

type handler struct {
	devices Devices
	store   Store
}

// NewHandler returns the device API routes. Every route requires a verified
// client certificate.
func NewHandler(devices Devices, store Store) http.Handler {
	h := &handler{devices: devices, store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /device/v1/register", h.register)
	mux.HandleFunc("POST /device/v1/heartbeat", h.heartbeat)
	mux.HandleFunc("POST /device/v1/ack", h.ack)
	mux.HandleFunc("GET /device/v1/state", h.state)
	return requireClientCert(mux)
}

// NewServer returns an HTTPS server for the device API. It allows TLS 1.2,
// unlike the gRPC listener's TLS 1.3, because that is what microcontroller
// TLS stacks (mbedTLS on the ESP32) negotiate. Start it with
// ListenAndServeTLS("", "").
func NewServer(addr, certDir string, h http.Handler) (*http.Server, error) {
	tlsCfg, err := mtls.ServerConfig(certDir, tls.VersionTLS12)
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, nil
}

type callerKey struct{}

func requireClientCert(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := mtls.PeerCommonName(r.TLS)
		if caller == "" {
			http.Error(w, "a verified client certificate is required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

func callerOf(r *http.Request) string {
	caller, _ := r.Context().Value(callerKey{}).(string)
	return caller
}

// ── Requests and responses ───────────────────────────────────────────────────

type registerReq struct {
	DeviceName   string            `json:"device_name"`
	Labels       map[string]string `json:"labels"`
	AgentVersion string            `json:"agent_version"`
	Arch         string            `json:"arch"`
	OS           string            `json:"os"`
	MemTotalMB   uint64            `json:"mem_total_mb"`
	Accelerators []string          `json:"accelerators"`
}

type deviceIDReq struct {
	DeviceID string `json:"device_id"`
}

type ackReq struct {
	DeploymentID string `json:"deployment_id"`
	DeviceID     string `json:"device_id"`
	Status       string `json:"status"`
	ErrorMsg     string `json:"error_msg"`
}

type stateResponse struct {
	ServerTsUnix int64              `json:"server_ts_unix"`
	Device       *model.Device      `json:"device"`
	Assignments  []model.Assignment `json:"assignments"`
	Fleet        fleetCounts        `json:"fleet"`
}

// fleetCounts is everything a device learns about the rest of the fleet:
// how many devices and deployments are in each status, never which ones.
type fleetCounts struct {
	Devices     map[string]int `json:"devices"`
	Deployments map[string]int `json:"deployments"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decode(w, r, &req) {
		return
	}
	resp, err := h.devices.RegisterAs(r.Context(), callerOf(r), &pb.RegisterRequest{
		DeviceName:   req.DeviceName,
		Labels:       req.Labels,
		AgentVersion: req.AgentVersion,
		Arch:         req.Arch,
		Os:           req.OS,
		MemTotalMb:   req.MemTotalMB,
		Accelerators: req.Accelerators,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]string{"device_id": resp.DeviceId})
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req deviceIDReq
	if !decode(w, r, &req) {
		return
	}
	ack, err := h.devices.HeartbeatAs(r.Context(), callerOf(r), &pb.Heartbeat{
		DeviceId: req.DeviceID,
		TsUnix:   time.Now().Unix(),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]int64{"server_ts_unix": ack.ServerTsUnix})
}

func (h *handler) ack(w http.ResponseWriter, r *http.Request) {
	var req ackReq
	if !decode(w, r, &req) {
		return
	}
	_, err := h.devices.AckAs(r.Context(), callerOf(r), &pb.DeploymentAck{
		DeploymentId: req.DeploymentID,
		DeviceId:     req.DeviceID,
		Status:       req.Status,
		ErrorMsg:     req.ErrorMsg,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// state is what a polling device shows: its own record, its newest
// deployments with its status on each, and fleet-wide counts. It reconciles
// first, so a deployment created while the device was away applies to it
// (or is recorded as skipped, with the reason) before it is read back.
func (h *handler) state(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deviceID := r.URL.Query().Get("device_id")
	if err := h.devices.Authorize(ctx, "State", callerOf(r), deviceID); err != nil {
		writeError(w, err)
		return
	}
	h.devices.Reconcile(ctx, deviceID)

	device, err := h.store.GetDevice(ctx, deviceID)
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	assignments, err := h.store.DeviceAssignments(ctx, deviceID, assignmentsLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if assignments == nil {
		assignments = []model.Assignment{}
	}
	resp := stateResponse{
		ServerTsUnix: time.Now().Unix(),
		Device:       device,
		Assignments:  assignments,
		Fleet:        fleetCounts{Devices: map[string]int{}, Deployments: map[string]int{}},
	}
	if counts, err := h.store.CountDevicesByStatus(ctx); err == nil {
		resp.Fleet.Devices = counts
	} else {
		log.Warn().Err(err).Msg("device state: count devices failed")
	}
	if counts, err := h.store.CountDeploymentsByStatus(ctx); err == nil {
		resp.Fleet.Deployments = counts
	} else {
		log.Warn().Err(err).Msg("device state: count deployments failed")
	}
	writeJSON(w, resp)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// decode reads a JSON body of at most 64 KiB, answering 400 on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// writeError maps the gRPC status the shared device logic returns to HTTP.
func writeError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch status.Code(err) {
	case codes.Unauthenticated:
		code = http.StatusUnauthorized
	case codes.PermissionDenied:
		code = http.StatusForbidden
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.NotFound:
		code = http.StatusNotFound
	}
	msg := err.Error()
	if s, ok := status.FromError(err); ok {
		msg = s.Message()
	}
	http.Error(w, msg, code)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn().Err(err).Msg("write json response failed")
	}
}
