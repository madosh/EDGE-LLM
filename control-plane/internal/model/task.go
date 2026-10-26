package model

import (
	"encoding/json"
	"time"
)

type TaskStatus string

const (
	TaskPending TaskStatus = "pending" // created, not yet delivered
	TaskRunning TaskStatus = "running" // sent to the device
	TaskDone    TaskStatus = "done"
	TaskFailed  TaskStatus = "failed"
)

// AgentTask is a question answered by the AI agent on one device. The agent
// may call read-only local tools; every call is recorded in Steps.
type AgentTask struct {
	ID          string          `json:"id"`
	DeviceID    string          `json:"device_id"`
	Prompt      string          `json:"prompt"`
	MaxSteps    int             `json:"max_steps"`
	Status      TaskStatus      `json:"status"`
	Answer      *string         `json:"answer"`
	ErrorMsg    *string         `json:"error_msg"`
	Steps       json.RawMessage `json:"steps"`
	ModelID     *string         `json:"model_id"`
	DurationMs  *int            `json:"duration_ms"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at"`
}

// IsTaskResultStatus reports whether a device may report s for a task.
func IsTaskResultStatus(s string) bool {
	return s == string(TaskDone) || s == string(TaskFailed)
}
