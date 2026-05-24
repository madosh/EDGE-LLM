package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cami-fleet/control-plane/internal/model"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	var pool *pgxpool.Pool
	var err error
	for i := 0; i < 15; i++ {
		pool, err = pgxpool.New(ctx, dsn)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS devices (
			id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			name             TEXT        NOT NULL UNIQUE,
			labels           JSONB       NOT NULL DEFAULT '{}',
			status           TEXT        NOT NULL DEFAULT 'offline',
			last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			current_model_id TEXT,
			agent_version    TEXT        NOT NULL DEFAULT '',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS deployments (
			id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id        TEXT        NOT NULL,
			artifact_url    TEXT        NOT NULL,
			artifact_sha256 TEXT        NOT NULL,
			tag_selector    JSONB       NOT NULL DEFAULT '{}',
			status          TEXT        NOT NULL DEFAULT 'pending',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at    TIMESTAMPTZ
		);

		CREATE TABLE IF NOT EXISTS device_deployments (
			id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			deployment_id UUID        NOT NULL REFERENCES deployments(id),
			device_id     UUID        NOT NULL REFERENCES devices(id),
			status        TEXT        NOT NULL DEFAULT 'pending',
			error_msg     TEXT,
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(deployment_id, device_id)
		);
	`)
	return err
}

// ── Device operations ──────────────────────────────────────────────────────────

func (s *Store) UpsertDevice(ctx context.Context, name string, labels map[string]string, agentVersion string) (string, error) {
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return "", fmt.Errorf("marshal labels: %w", err)
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO devices (name, labels, agent_version, status, last_seen_at)
		VALUES ($1, $2, $3, 'online', NOW())
		ON CONFLICT (name) DO UPDATE
		  SET labels        = EXCLUDED.labels,
		      agent_version = EXCLUDED.agent_version,
		      status        = 'online',
		      last_seen_at  = NOW()
		RETURNING id
	`, name, labelsJSON, agentVersion).Scan(&id)
	return id, err
}

func (s *Store) TouchHeartbeat(ctx context.Context, deviceID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET status = 'online', last_seen_at = NOW()
		WHERE id = $1
	`, deviceID)
	return err
}

func (s *Store) MarkStaleDevicesOffline(ctx context.Context, threshold time.Duration) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE devices SET status = 'offline'
		WHERE status = 'online' AND last_seen_at < NOW() - $1::interval
		RETURNING id
	`, threshold.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Store) ListDevices(ctx context.Context) ([]model.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, created_at
		FROM devices ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDevices(rows)
}

func (s *Store) GetDevice(ctx context.Context, id string) (*model.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, created_at
		FROM devices WHERE id = $1
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devs, err := scanDevices(rows)
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, errors.New("device not found")
	}
	return &devs[0], nil
}

func (s *Store) FindDevicesByLabels(ctx context.Context, selector map[string]string) ([]model.Device, error) {
	selectorJSON, err := json.Marshal(selector)
	if err != nil {
		return nil, fmt.Errorf("marshal selector: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, created_at
		FROM devices WHERE labels @> $1
	`, selectorJSON)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDevices(rows)
}

func (s *Store) UpdateDeviceModel(ctx context.Context, deviceID, modelID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devices SET current_model_id = $2 WHERE id = $1`, deviceID, modelID)
	return err
}

func scanDevices(rows pgx.Rows) ([]model.Device, error) {
	var devices []model.Device
	for rows.Next() {
		var d model.Device
		var labelsJSON []byte
		if err := rows.Scan(&d.ID, &d.Name, &labelsJSON, &d.Status,
			&d.LastSeenAt, &d.CurrentModelID, &d.AgentVersion, &d.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labelsJSON, &d.Labels); err != nil {
			d.Labels = map[string]string{}
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ── Deployment operations ──────────────────────────────────────────────────────

func (s *Store) CreateDeployment(ctx context.Context, modelID, artifactURL, artifactSHA256 string, tagSelector map[string]string) (*model.Deployment, error) {
	tagJSON, err := json.Marshal(tagSelector)
	if err != nil {
		return nil, fmt.Errorf("marshal tag_selector: %w", err)
	}
	var d model.Deployment
	err = s.pool.QueryRow(ctx, `
		INSERT INTO deployments (model_id, artifact_url, artifact_sha256, tag_selector)
		VALUES ($1, $2, $3, $4)
		RETURNING id, model_id, artifact_url, artifact_sha256, tag_selector, status, created_at, completed_at
	`, modelID, artifactURL, artifactSHA256, tagJSON).Scan(
		&d.ID, &d.ModelID, &d.ArtifactURL, &d.ArtifactSHA256,
		&tagJSON, &d.Status, &d.CreatedAt, &d.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tagJSON, &d.TagSelector); err != nil {
		d.TagSelector = map[string]string{}
	}
	return &d, nil
}

func (s *Store) CreateDeviceDeployment(ctx context.Context, deploymentID, deviceID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_deployments (deployment_id, device_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, deploymentID, deviceID)
	return err
}

func (s *Store) GetPendingDeployments(ctx context.Context, deviceID string) ([]model.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.model_id, d.artifact_url, d.artifact_sha256
		FROM deployments d
		JOIN device_deployments dd ON dd.deployment_id = d.id
		WHERE dd.device_id = $1 AND dd.status IN ('pending')
	`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deployments []model.Deployment
	for rows.Next() {
		var dep model.Deployment
		if err := rows.Scan(&dep.ID, &dep.ModelID, &dep.ArtifactURL, &dep.ArtifactSHA256); err != nil {
			return nil, err
		}
		deployments = append(deployments, dep)
	}
	return deployments, rows.Err()
}

func (s *Store) UpdateDeviceDeploymentStatus(ctx context.Context, deploymentID, deviceID, status, errorMsg string) error {
	var errMsgPtr *string
	if errorMsg != "" {
		errMsgPtr = &errorMsg
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE device_deployments
		SET status = $3, error_msg = $4, updated_at = NOW()
		WHERE deployment_id = $1 AND device_id = $2
	`, deploymentID, deviceID, status, errMsgPtr)
	return err
}

func (s *Store) ListDeployments(ctx context.Context) ([]model.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, model_id, artifact_url, artifact_sha256, tag_selector, status, created_at, completed_at
		FROM deployments ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deployments []model.Deployment
	for rows.Next() {
		var d model.Deployment
		var tagJSON []byte
		if err := rows.Scan(&d.ID, &d.ModelID, &d.ArtifactURL, &d.ArtifactSHA256,
			&tagJSON, &d.Status, &d.CreatedAt, &d.CompletedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tagJSON, &d.TagSelector); err != nil {
			d.TagSelector = map[string]string{}
		}
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach device statuses
	for i := range deployments {
		devices, err := s.listDeviceDeployments(ctx, deployments[i].ID)
		if err == nil {
			deployments[i].Devices = devices
		}
	}
	return deployments, nil
}

func (s *Store) GetDeployment(ctx context.Context, id string) (*model.Deployment, error) {
	var d model.Deployment
	var tagJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, model_id, artifact_url, artifact_sha256, tag_selector, status, created_at, completed_at
		FROM deployments WHERE id = $1
	`, id).Scan(&d.ID, &d.ModelID, &d.ArtifactURL, &d.ArtifactSHA256,
		&tagJSON, &d.Status, &d.CreatedAt, &d.CompletedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tagJSON, &d.TagSelector); err != nil {
		d.TagSelector = map[string]string{}
	}
	return &d, nil
}

func (s *Store) listDeviceDeployments(ctx context.Context, deploymentID string) ([]model.DeviceDeployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT dd.deployment_id, dd.device_id, d.name, dd.status, dd.error_msg, dd.updated_at
		FROM device_deployments dd
		JOIN devices d ON d.id = dd.device_id
		WHERE dd.deployment_id = $1
	`, deploymentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.DeviceDeployment
	for rows.Next() {
		var dd model.DeviceDeployment
		if err := rows.Scan(&dd.DeploymentID, &dd.DeviceID, &dd.DeviceName,
			&dd.Status, &dd.ErrorMsg, &dd.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, dd)
	}
	return items, rows.Err()
}

// CheckDeploymentCompletion marks the deployment as completed/failed/partial_failure
// when all device_deployments have reached a terminal state.
func (s *Store) CheckDeploymentCompletion(ctx context.Context, deploymentID string) error {
	var total, running, failed int
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM device_deployments WHERE deployment_id = $1
	`, deploymentID).Scan(&total, &running, &failed)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	if running+failed < total {
		return nil
	}
	var newStatus string
	switch {
	case failed == 0:
		newStatus = "completed"
	case running == 0:
		newStatus = "failed"
	default:
		newStatus = "partial_failure"
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE deployments SET status = $2, completed_at = NOW()
		WHERE id = $1
	`, deploymentID, newStatus)
	return err
}

// GenerateUUID returns a new random UUID string.
func GenerateUUID() string {
	return uuid.New().String()
}
