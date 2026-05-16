package rest

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	pb "github.com/cami-fleet/control-plane/gen"
	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
	natsclient "github.com/cami-fleet/control-plane/internal/events/nats"
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
	ModelID        string            `json:"model_id"`
	ArtifactURL    string            `json:"artifact_url"`
	ArtifactSHA256 string            `json:"artifact_sha256"`
	TagSelector    map[string]string `json:"tag_selector"`
}

func (h *Handlers) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var req createDeploymentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ModelID == "" || req.ArtifactURL == "" || req.ArtifactSHA256 == "" {
		http.Error(w, "model_id, artifact_url, artifact_sha256 required", http.StatusBadRequest)
		return
	}
	if req.TagSelector == nil {
		req.TagSelector = map[string]string{}
	}

	ctx := r.Context()

	deployment, err := h.pg.CreateDeployment(ctx, req.ModelID, req.ArtifactURL, req.ArtifactSHA256, req.TagSelector)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Find matching devices (empty selector → match all)
	devices, err := h.pg.FindDevicesByLabels(ctx, req.TagSelector)
	if err != nil {
		log.Warn().Err(err).Msg("find devices by labels failed")
	}

	for _, device := range devices {
		if err := h.pg.CreateDeviceDeployment(ctx, deployment.ID, device.ID); err != nil {
			log.Warn().Err(err).Str("device_id", device.ID).Msg("create device_deployment failed")
			continue
		}

		instr := &pb.DeploymentInstruction{
			DeploymentId:   deployment.ID,
			ModelId:        req.ModelID,
			ArtifactUrl:    req.ArtifactURL,
			ArtifactSha256: req.ArtifactSHA256,
		}

		// Push to any live WatchDeployments streams for this device
		h.grpcSrv.PushDeployment(device.ID, instr)

		// Also publish on NATS so other control-plane instances / future consumers see it
		if h.nats != nil {
			type natsDeploy struct {
				DeploymentID   string `json:"deployment_id"`
				ModelID        string `json:"model_id"`
				ArtifactURL    string `json:"artifact_url"`
				ArtifactSHA256 string `json:"artifact_sha256"`
			}
			h.nats.Publish(natsclient.SubjectDeployment(device.ID), natsDeploy{
				DeploymentID:   deployment.ID,
				ModelID:        req.ModelID,
				ArtifactURL:    req.ArtifactURL,
				ArtifactSHA256: req.ArtifactSHA256,
			})
		}
	}

	deployment.Devices = nil
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, deployment)
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn().Err(err).Msg("write json response failed")
	}
}
