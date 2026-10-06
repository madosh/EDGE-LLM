package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	pb "github.com/cami-fleet/control-plane/gen"
	"github.com/cami-fleet/control-plane/internal/fleet"
	"github.com/cami-fleet/control-plane/internal/model"
	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

// ── Staged rollout ───────────────────────────────────────────────────────────

type promoteReq struct {
	RolloutPercent int `json:"rollout_percent"`
}

// PromoteDeployment raises a deployment's rollout percentage (e.g. a 10%
// canary to 100%) and sends it to the devices that are now included.
func (h *Handlers) PromoteDeployment(w http.ResponseWriter, r *http.Request) {
	var req promoteReq
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.RolloutPercent < 1 || req.RolloutPercent > 100 {
		http.Error(w, "rollout_percent must be between 1 and 100", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if err := h.pg.SetRolloutPercent(ctx, id, req.RolloutPercent); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	d, err := h.pg.GetDeployment(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	summary := h.targetMatching(ctx, d)
	if len(summary.Targeted) > 0 {
		if err := h.pg.ReopenDeployment(ctx, d.ID); err != nil {
			log.Warn().Err(err).Msg("reopen deployment failed")
		}
	}
	writeJSON(w, deploymentResponse{Deployment: d, Targeting: summary})
}

type rollbackResponse struct {
	DeploymentID string `json:"deployment_id"`
	// Restored maps device ID -> the deployment it is going back to.
	Restored map[string]string `json:"restored"`
	// NoPrevious lists devices that never ran an earlier model; they keep
	// whatever is loaded until a new deployment reaches them.
	NoPrevious []string `json:"no_previous"`
}

// RollbackDeployment stops a deployment and sends every device it reached
// back to the model it ran before. The previous model is usually still in the
// device's verified cache, so the rollback needs no download.
func (h *Handlers) RollbackDeployment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	devices, err := h.pg.RollBackDeployment(ctx, id)
	if errors.Is(err, pgstore.ErrNotRollbackable) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := rollbackResponse{DeploymentID: id, Restored: map[string]string{}, NoPrevious: []string{}}
	for _, deviceID := range devices {
		prev, err := h.pg.PreviousRunningDeployment(ctx, deviceID, id)
		if err != nil {
			log.Warn().Err(err).Str("device_id", deviceID).Msg("find previous deployment failed")
			continue
		}
		if prev == nil {
			resp.NoPrevious = append(resp.NoPrevious, deviceID)
			continue
		}
		if err := h.pg.ResetDeviceDeployment(ctx, prev.ID, deviceID); err != nil {
			log.Warn().Err(err).Str("device_id", deviceID).Msg("reset previous deployment failed")
			continue
		}
		if err := h.pg.ReopenDeployment(ctx, prev.ID); err != nil {
			log.Warn().Err(err).Msg("reopen previous deployment failed")
		}
		h.grpcSrv.PushDeployment(deviceID, fleet.Instruction(prev))
		h.publishDeployment(deviceID, prev)
		resp.Restored[deviceID] = prev.ID
	}
	writeJSON(w, resp)
}

// ── On-device AI agent tasks ─────────────────────────────────────────────────

const (
	maxPromptLen    = 4000
	defaultMaxSteps = 4
	maxMaxSteps     = 8
)

type createTaskReq struct {
	Prompt   string `json:"prompt"`
	MaxSteps int    `json:"max_steps"`
}

// CreateTask asks the AI agent on one device a question. The device answers
// with the model it is running, and may call read-only local tools first.
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskReq
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" || len(req.Prompt) > maxPromptLen {
		http.Error(w, "prompt is required and must be at most 4000 characters", http.StatusBadRequest)
		return
	}
	if req.MaxSteps == 0 {
		req.MaxSteps = defaultMaxSteps
	}
	if req.MaxSteps < 1 || req.MaxSteps > maxMaxSteps {
		http.Error(w, "max_steps must be between 1 and 8", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	deviceID := chi.URLParam(r, "id")
	if _, err := h.pg.GetDevice(ctx, deviceID); err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	task, err := h.pg.CreateTask(ctx, deviceID, req.Prompt, req.MaxSteps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// An offline device gets the task when it reconnects.
	if h.grpcSrv.PushTask(deviceID, &pb.AgentTask{TaskId: task.ID, Prompt: task.Prompt, MaxSteps: uint32(task.MaxSteps)}) {
		if err := h.pg.MarkTaskRunning(ctx, task.ID); err == nil {
			task.Status = model.TaskRunning
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, task)
}

func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.pg.ListTasks(r.Context(), chi.URLParam(r, "id"), 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []model.AgentTask{}
	}
	writeJSON(w, tasks)
}

func (h *Handlers) GetTask(w http.ResponseWriter, r *http.Request) {
	task, err := h.pg.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	writeJSON(w, task)
}
