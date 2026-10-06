package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
	"github.com/cami-fleet/control-plane/internal/fleet"
	"github.com/cami-fleet/control-plane/internal/model"
	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

type Handlers struct {
	pg           *pgstore.Store
	ch           *chstore.Store
	grpcSrv      *grpcapi.Server
	nats         *natsclient.Client
	artifactsDir string
	artifactsURL string // e.g. "http://control-plane:8080"
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handlers) ListArtifacts(w http.ResponseWriter, r *http.Request) {
	arts := discoverArtifacts(h.artifactsDir, h.artifactsURL)
	if arts == nil {
		arts = []artifactEntry{}
	}
	writeJSON(w, arts)
}

func (h *Handlers) ListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.pg.ListDevices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if devices == nil {
		devices = []model.Device{}
	}
	writeJSON(w, devices)
}

func (h *Handlers) GetDevice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	device, err := h.pg.GetDevice(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, device)
}

func (h *Handlers) GetDeviceTelemetry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit := 60
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	points, err := h.ch.QueryTelemetry(r.Context(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if points == nil {
		points = []chstore.TelemetryPoint{}
	}
	writeJSON(w, points)
}

type createDeploymentReq struct {
	ModelID        string             `json:"model_id"`
	ArtifactURL    string             `json:"artifact_url"`
	ArtifactSHA256 string             `json:"artifact_sha256"`
	TagSelector    map[string]string  `json:"tag_selector"`
	RolloutPercent int                `json:"rollout_percent"` // 1–100, default 100
	Requirements   model.Requirements `json:"requirements"`
	// AllDevices must be true to deploy with an empty selector, so a
	// forgotten selector cannot silently target the whole fleet.
	AllDevices bool `json:"all_devices"`
}

func (req *createDeploymentReq) validate() error {
	if req.ModelID == "" || req.ArtifactURL == "" || req.ArtifactSHA256 == "" {
		return errors.New("model_id, artifact_url, artifact_sha256 required")
	}
	if !isSHA256Hex(req.ArtifactSHA256) {
		return errors.New("artifact_sha256 must be 64 hex characters")
	}
	if len(req.TagSelector) == 0 && !req.AllDevices {
		return errors.New("tag_selector is empty: set all_devices=true to deploy to every device")
	}
	if req.RolloutPercent == 0 {
		req.RolloutPercent = 100
	}
	if req.RolloutPercent < 1 || req.RolloutPercent > 100 {
		return errors.New("rollout_percent must be between 1 and 100")
	}
	return req.Requirements.Validate()
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// decodeJSON reads a request body of at most 1 MiB.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

// deploymentResponse is a deployment plus what happened per device. The
// deployment's own fields stay at the top level of the JSON.
type deploymentResponse struct {
	*model.Deployment
	Targeting *fleet.Summary `json:"targeting"`
}

func (h *Handlers) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req createDeploymentReq
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.TagSelector == nil {
		req.TagSelector = map[string]string{}
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	deployment, err := h.pg.CreateDeploymentSpec(ctx, pgstore.DeploymentSpec{
		ModelID:        req.ModelID,
		ArtifactURL:    req.ArtifactURL,
		ArtifactSHA256: strings.ToLower(req.ArtifactSHA256),
		TagSelector:    req.TagSelector,
		RolloutPercent: req.RolloutPercent,
		Requirements:   req.Requirements,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	summary := h.targetMatching(ctx, deployment)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, deploymentResponse{Deployment: deployment, Targeting: summary})
}

// targetMatching applies a deployment to every device whose labels match,
// subject to the rollout percentage and the requirements.
func (h *Handlers) targetMatching(ctx context.Context, d *model.Deployment) *fleet.Summary {
	summary := fleet.NewSummary()
	devices, err := h.pg.FindDevicesByLabels(ctx, d.TagSelector)
	if err != nil {
		log.Warn().Err(err).Msg("find devices by labels failed")
		return summary
	}
	for _, device := range devices {
		outcome, reason, err := fleet.Target(ctx, h.pg, h.grpcSrv, d, device, true)
		if err != nil {
			log.Warn().Err(err).Str("device_id", device.ID).Msg("target device failed")
			continue
		}
		summary.Add(device.ID, outcome, reason)
		if outcome == fleet.Targeted {
			h.publishDeployment(device.ID, d)
		}
	}
	return summary
}

// publishDeployment emits an event for observers (the dashboard's event
// stream). Devices do not listen on NATS, so this never delivers twice.
func (h *Handlers) publishDeployment(deviceID string, d *model.Deployment) {
	if h.nats == nil {
		return
	}
	h.nats.Publish(natsclient.SubjectDeployment(deviceID), map[string]string{
		"deployment_id":   d.ID,
		"model_id":        d.ModelID,
		"artifact_url":    d.ArtifactURL,
		"artifact_sha256": d.ArtifactSHA256,
	})
}

func (h *Handlers) ListDeployments(w http.ResponseWriter, r *http.Request) {
	deployments, err := h.pg.ListDeployments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if deployments == nil {
		deployments = []model.Deployment{}
	}
	writeJSON(w, deployments)
}

func (h *Handlers) GetDeployment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dep, err := h.pg.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, dep)
}

// ── Fleet Telemetry Handlers ─────────────────────────────────────────────────

func (h *Handlers) GetFleetSummary(w http.ResponseWriter, r *http.Request) {
	window := parseDuration(r.URL.Query().Get("window"), 5*time.Minute)
	summary, err := h.ch.QueryFleetSummary(r.Context(), window)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, summary)
}

func (h *Handlers) GetFleetTimeSeries(w http.ResponseWriter, r *http.Request) {
	window := parseDuration(r.URL.Query().Get("window"), 30*time.Minute)
	bucket := 30 // seconds
	if b := r.URL.Query().Get("bucket"); b != "" {
		if n, err := strconv.Atoi(b); err == nil && n > 0 {
			bucket = n
		}
	}
	series, err := h.ch.QueryFleetTimeSeries(r.Context(), window, bucket)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if series == nil {
		series = []chstore.FleetTimeSeries{}
	}
	writeJSON(w, series)
}

func (h *Handlers) GetFleetDevices(w http.ResponseWriter, r *http.Request) {
	metrics, err := h.ch.QueryFleetLatest(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metrics == nil {
		metrics = []chstore.DeviceMetric{}
	}
	writeJSON(w, metrics)
}

func (h *Handlers) GetTelemetryAlerts(w http.ResponseWriter, r *http.Request) {
	threshold := 10.0
	if t := r.URL.Query().Get("threshold"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil && v > 0 {
			threshold = v
		}
	}
	slow, err := h.ch.QuerySlowDevices(r.Context(), threshold)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if slow == nil {
		slow = []chstore.DeviceMetric{}
	}
	writeJSON(w, slow)
}

// SSEStream pushes real-time events to browser clients via Server-Sent Events.
func (h *Handlers) SSEStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	eventCh := make(chan []byte, 64)

	// Subscribe to all device events via NATS
	sub1, _ := h.nats.Subscribe("device.>", func(data []byte) {
		select {
		case eventCh <- data:
		default:
		}
	})
	sub2, _ := h.nats.Subscribe("deployment.>", func(data []byte) {
		select {
		case eventCh <- data:
		default:
		}
	})
	defer func() {
		if sub1 != nil {
			sub1.Unsubscribe()
		}
		if sub2 != nil {
			sub2.Unsubscribe()
		}
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-eventCh:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn().Err(err).Msg("write json response failed")
	}
}
