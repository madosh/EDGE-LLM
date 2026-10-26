package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/cami-fleet/control-plane/internal/model"
)

// ErrNotRollbackable means the deployment does not exist or was already rolled back.
var ErrNotRollbackable = errors.New("deployment not found or already rolled back")

// prefixed returns deploymentCols qualified with a table alias.
func prefixed(alias string) string {
	cols := strings.Split(deploymentCols, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// MatchingDeployments returns the deployments that are not rolled back and
// whose tag selector the device's labels satisfy, newest first. The newest
// one that includes the device in its rollout is what the device should run.
func (s *Store) MatchingDeployments(ctx context.Context, deviceID string) ([]model.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixed("d")+`
		FROM deployments d
		JOIN devices v ON v.id = $1
		WHERE v.labels @> d.tag_selector AND d.status <> 'rolled_back'
		ORDER BY d.created_at DESC
		LIMIT 50
	`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// TargetDevice records that a deployment applies to a device, as pending or
// (with a reason) skipped. It reports whether a new row was created; an
// existing row is left untouched, so calling it again is harmless.
func (s *Store) TargetDevice(ctx context.Context, deploymentID, deviceID string, status model.DeviceDeploymentStatus, reason string) (bool, error) {
	var errMsg *string
	if reason != "" {
		errMsg = &reason
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO device_deployments (deployment_id, device_id, status, error_msg)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (deployment_id, device_id) DO NOTHING
		RETURNING id
	`, deploymentID, deviceID, string(status), errMsg).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// SetRolloutPercent raises a deployment's rollout. Lowering it is refused:
// devices already running the model would not be rolled back by it.
func (s *Store) SetRolloutPercent(ctx context.Context, deploymentID string, percent int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments SET rollout_percent = $2
		WHERE id = $1 AND status <> 'rolled_back' AND rollout_percent <= $2
	`, deploymentID, percent)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deployment not found, rolled back, or already above %d%%", percent)
	}
	return nil
}

// ReopenDeployment moves a finished deployment back to in_progress, after a
// promotion or reconnect added a device that has not run it yet.
func (s *Store) ReopenDeployment(ctx context.Context, deploymentID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET status = 'in_progress', completed_at = NULL
		WHERE id = $1 AND status IN ('completed', 'failed', 'partial_failure')
	`, deploymentID)
	return err
}

// RetrySkipped turns a skipped device back into a pending one, for when a
// device that did not meet the requirements now reports that it does.
func (s *Store) RetrySkipped(ctx context.Context, deploymentID, deviceID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE device_deployments SET status = 'pending', error_msg = NULL, updated_at = NOW()
		WHERE deployment_id = $1 AND device_id = $2 AND status = 'skipped'
	`, deploymentID, deviceID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RollBackDeployment marks a deployment rolled back and returns the devices
// it was applied to (pending, in progress or running), now marked rolled back.
func (s *Store) RollBackDeployment(ctx context.Context, deploymentID string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	tag, err := tx.Exec(ctx, `
		UPDATE deployments SET status = 'rolled_back', completed_at = NOW()
		WHERE id = $1 AND status <> 'rolled_back'
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotRollbackable
	}
	rows, err := tx.Query(ctx, `
		UPDATE device_deployments SET status = 'rolled_back', updated_at = NOW()
		WHERE deployment_id = $1 AND status IN ('pending', 'downloading', 'verifying', 'running')
		RETURNING device_id
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	var devices []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		devices = append(devices, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return devices, tx.Commit(ctx)
}

// PreviousRunningDeployment is the newest deployment, created before
// deploymentID and not rolled back, that the device was running. Rolling back
// returns the device to it. Returns (nil, nil) when there is none.
func (s *Store) PreviousRunningDeployment(ctx context.Context, deviceID, deploymentID string) (*model.Deployment, error) {
	d, err := scanDeployment(s.pool.QueryRow(ctx, `
		SELECT `+prefixed("d")+`
		FROM deployments d
		JOIN device_deployments dd ON dd.deployment_id = d.id
		WHERE dd.device_id = $1
		  AND dd.status = 'running'
		  AND d.id <> $2
		  AND d.status <> 'rolled_back'
		  AND d.created_at < (SELECT created_at FROM deployments WHERE id = $2)
		ORDER BY d.created_at DESC
		LIMIT 1
	`, deviceID, deploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// ResetDeviceDeployment sets a device's row back to pending so the
// instruction is sent again (now and on every reconnect until it runs).
func (s *Store) ResetDeviceDeployment(ctx context.Context, deploymentID, deviceID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE device_deployments SET status = 'pending', error_msg = NULL, updated_at = NOW()
		WHERE deployment_id = $1 AND device_id = $2
	`, deploymentID, deviceID)
	return err
}
