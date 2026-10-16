package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cami-fleet/control-plane/internal/model"
)

// ErrTaskNotFound means no task with that ID belongs to that device.
var ErrTaskNotFound = errors.New("task not found for this device")

const taskCols = "id, device_id, prompt, max_steps, status, answer, error_msg, steps, model_id, duration_ms, created_at, completed_at"

func scanTask(row rowScanner) (*model.AgentTask, error) {
	var t model.AgentTask
	var steps []byte
	if err := row.Scan(&t.ID, &t.DeviceID, &t.Prompt, &t.MaxSteps, &t.Status, &t.Answer,
		&t.ErrorMsg, &steps, &t.ModelID, &t.DurationMs, &t.CreatedAt, &t.CompletedAt); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		steps = []byte("[]")
	}
	t.Steps = json.RawMessage(steps)
	return &t, nil
}

func (s *Store) CreateTask(ctx context.Context, deviceID, prompt string, maxSteps int) (*model.AgentTask, error) {
	return scanTask(s.pool.QueryRow(ctx, `
		INSERT INTO agent_tasks (device_id, prompt, max_steps)
		VALUES ($1, $2, $3)
		RETURNING `+taskCols, deviceID, prompt, maxSteps))
}

func (s *Store) GetTask(ctx context.Context, id string) (*model.AgentTask, error) {
	return scanTask(s.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM agent_tasks WHERE id = $1`, id))
}

func (s *Store) ListTasks(ctx context.Context, deviceID string, limit int) ([]model.AgentTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskCols+` FROM agent_tasks WHERE device_id = $1
		ORDER BY created_at DESC LIMIT $2
	`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AgentTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// OpenTasks returns the device's tasks that have no result yet, oldest first,
// so they can be (re-)sent when the device connects.
func (s *Store) OpenTasks(ctx context.Context, deviceID string) ([]model.AgentTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskCols+` FROM agent_tasks
		WHERE device_id = $1 AND status IN ('pending', 'running')
		ORDER BY created_at
	`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AgentTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) MarkTaskRunning(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE agent_tasks SET status = 'running' WHERE id = $1 AND status = 'pending'`, id)
	return err
}

// TaskResult is what a device reports when it has answered (or failed).
type TaskResult struct {
	Status     model.TaskStatus
	Answer     string
	ErrorMsg   string
	Steps      json.RawMessage
	ModelID    string
	DurationMs int
}

// CompleteTask stores a device's result. It only touches a task that belongs
// to deviceID and has no result yet, so one device cannot answer for another.
func (s *Store) CompleteTask(ctx context.Context, id, deviceID string, r TaskResult) error {
	if len(r.Steps) == 0 || !json.Valid(r.Steps) {
		r.Steps = json.RawMessage("[]")
	}
	var answer, errMsg, modelID *string
	if r.Answer != "" {
		answer = &r.Answer
	}
	if r.ErrorMsg != "" {
		errMsg = &r.ErrorMsg
	}
	if r.ModelID != "" {
		modelID = &r.ModelID
	}
	var got string
	err := s.pool.QueryRow(ctx, `
		UPDATE agent_tasks
		SET status = $3, answer = $4, error_msg = $5, steps = $6, model_id = $7,
		    duration_ms = $8, completed_at = NOW()
		WHERE id = $1 AND device_id = $2 AND status IN ('pending', 'running')
		RETURNING id
	`, id, deviceID, string(r.Status), answer, errMsg, []byte(r.Steps), modelID, r.DurationMs).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTaskNotFound
	}
	return err
}
