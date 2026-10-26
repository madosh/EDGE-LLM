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

		-- Platform reported at registration; used for capability-aware targeting.
		ALTER TABLE devices ADD COLUMN IF NOT EXISTS arch TEXT NOT NULL DEFAULT '';
		ALTER TABLE devices ADD COLUMN IF NOT EXISTS os   TEXT NOT NULL DEFAULT '';

		-- Reconnect lookup (device_id + status) and selector matching (labels @> ...).
		CREATE INDEX IF NOT EXISTS idx_device_deployments_device_status
			ON device_deployments (device_id, status);
		CREATE INDEX IF NOT EXISTS idx_devices_labels
			ON devices USING GIN (labels jsonb_path_ops);

		-- Hardware a device reports, matched against a deployment's requirements.
		ALTER TABLE devices ADD COLUMN IF NOT EXISTS mem_total_mb BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE devices ADD COLUMN IF NOT EXISTS accelerators JSONB  NOT NULL DEFAULT '[]';

		-- Staged rollout and per-model requirements.
		ALTER TABLE deployments ADD COLUMN IF NOT EXISTS rollout_percent INT   NOT NULL DEFAULT 100;
		ALTER TABLE deployments ADD COLUMN IF NOT EXISTS requirements    JSONB NOT NULL DEFAULT '{}';
		CREATE INDEX IF NOT EXISTS idx_deployments_selector
			ON deployments USING GIN (tag_selector jsonb_path_ops);

		-- Questions answered by the AI agent on a device, with its tool calls.
		CREATE TABLE IF NOT EXISTS agent_tasks (
			id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			device_id    UUID        NOT NULL REFERENCES devices(id),
			prompt       TEXT        NOT NULL,
			max_steps    INT         NOT NULL DEFAULT 4,
			status       TEXT        NOT NULL DEFAULT 'pending',
			answer       TEXT,
			error_msg    TEXT,
			steps        JSONB       NOT NULL DEFAULT '[]',
			model_id     TEXT,
			duration_ms  INT,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_agent_tasks_device ON agent_tasks (device_id, created_at DESC);
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
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, arch, os, mem_total_mb, accelerators, created_at
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
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, arch, os, mem_total_mb, accelerators, created_at
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
		SELECT id, name, labels, status, last_seen_at, current_model_id, agent_version, arch, os, mem_total_mb, accelerators, created_at
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

// UpdateDeviceCapabilities records the hardware a device reported at
// registration: CPU architecture, OS, total RAM and accelerators.
func (s *Store) UpdateDeviceCapabilities(ctx context.Context, deviceID, arch, os string, memTotalMB int64, accelerators []string) error {
	if accelerators == nil {
		accelerators = []string{}
	}
	accelJSON, err := json.Marshal(accelerators)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE devices SET arch = $2, os = $3, mem_total_mb = $4, accelerators = $5 WHERE id = $1
	`, deviceID, arch, os, memTotalMB, accelJSON)
	return err
}

// DeviceName returns the registered name of a device, which is also the
// common name on that device's client certificate.
func (s *Store) DeviceName(ctx context.Context, deviceID string) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx, `SELECT name FROM devices WHERE id = $1`, deviceID).Scan(&name)
	return name, err
}

// DeploymentHasDevice reports whether deviceID is a target of deploymentID.
func (s *Store) DeploymentHasDevice(ctx context.Context, deploymentID, deviceID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM device_deployments WHERE deployment_id = $1 AND device_id = $2
		)
	`, deploymentID, deviceID).Scan(&ok)
	return ok, err
}

// CountDevicesByStatus returns the number of devices per status.
func (s *Store) CountDevicesByStatus(ctx context.Context) (map[string]int, error) {
	return s.countBy(ctx, `SELECT status, COUNT(*) FROM devices GROUP BY status`)
}

// CountDeploymentsByStatus returns the number of deployments per status.
func (s *Store) CountDeploymentsByStatus(ctx context.Context) (map[string]int, error) {
	return s.countBy(ctx, `SELECT status, COUNT(*) FROM deployments GROUP BY status`)
}

func (s *Store) countBy(ctx context.Context, query string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		counts[key] = n
	}
	return counts, rows.Err()
}

func scanDevices(rows pgx.Rows) ([]model.Device, error) {
	var devices []model.Device
	for rows.Next() {
		var d model.Device
		var labelsJSON, accelJSON []byte
		if err := rows.Scan(&d.ID, &d.Name, &labelsJSON, &d.Status,
			&d.LastSeenAt, &d.CurrentModelID, &d.AgentVersion, &d.Arch, &d.OS,
			&d.MemTotalMB, &accelJSON, &d.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labelsJSON, &d.Labels); err != nil {
			d.Labels = map[string]string{}
		}
		if err := json.Unmarshal(accelJSON, &d.Accelerators); err != nil || d.Accelerators == nil {
			d.Accelerators = []string{}
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// ── Deployment operations ──────────────────────────────────────────────────────

// deploymentCols is the column list every deployment query selects, in the
// order scanDeployment reads them.
const deploymentCols = "id, model_id, artifact_url, artifact_sha256, tag_selector, rollout_percent, requirements, status, created_at, completed_at"

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDeployment(row rowScanner) (*model.Deployment, error) {
	var d model.Deployment
	var tagJSON, reqJSON []byte
	if err := row.Scan(&d.ID, &d.ModelID, &d.ArtifactURL, &d.ArtifactSHA256,
		&tagJSON, &d.RolloutPercent, &reqJSON, &d.Status, &d.CreatedAt, &d.CompletedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tagJSON, &d.TagSelector); err != nil || d.TagSelector == nil {
		d.TagSelector = map[string]string{}
	}
	if err := json.Unmarshal(reqJSON, &d.Requirements); err != nil {
		d.Requirements = model.Requirements{}
	}
	return &d, nil
}

// DeploymentSpec is what an operator asks for when creating a deployment.
type DeploymentSpec struct {
	ModelID        string
	ArtifactURL    string
	ArtifactSHA256 string
	TagSelector    map[string]string
	RolloutPercent int // 1–100; 0 means 100
	Requirements   model.Requirements
}

// CreateDeployment creates a deployment for every matching device, with no
// requirements. See CreateDeploymentSpec for staged rollouts.
func (s *Store) CreateDeployment(ctx context.Context, modelID, artifactURL, artifactSHA256 string, tagSelector map[string]string) (*model.Deployment, error) {
	return s.CreateDeploymentSpec(ctx, DeploymentSpec{
		ModelID:        modelID,
		ArtifactURL:    artifactURL,
		ArtifactSHA256: artifactSHA256,
		TagSelector:    tagSelector,
	})
}

func (s *Store) CreateDeploymentSpec(ctx context.Context, spec DeploymentSpec) (*model.Deployment, error) {
	if spec.TagSelector == nil {
		spec.TagSelector = map[string]string{}
	}
	if spec.RolloutPercent <= 0 {
		spec.RolloutPercent = 100
	}
	tagJSON, err := json.Marshal(spec.TagSelector)
	if err != nil {
		return nil, fmt.Errorf("marshal tag_selector: %w", err)
	}
	reqJSON, err := json.Marshal(spec.Requirements)
	if err != nil {
		return nil, fmt.Errorf("marshal requirements: %w", err)
	}
	return scanDeployment(s.pool.QueryRow(ctx, `
		INSERT INTO deployments (model_id, artifact_url, artifact_sha256, tag_selector, rollout_percent, requirements)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+deploymentCols,
		spec.ModelID, spec.ArtifactURL, spec.ArtifactSHA256, tagJSON, spec.RolloutPercent, reqJSON))
}

func (s *Store) CreateDeviceDeployment(ctx context.Context, deploymentID, deviceID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_deployments (deployment_id, device_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING
	`, deploymentID, deviceID)
	return err
}

// GetPendingDeployments returns deployments this device has not finished:
// never started, or interrupted mid-download or mid-verify (e.g. the agent
// crashed). Re-sending the in-flight ones stops a crash leaving a device stuck
// in "downloading" forever; the agent skips IDs it is already working on.
func (s *Store) GetPendingDeployments(ctx context.Context, deviceID string) ([]model.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.model_id, d.artifact_url, d.artifact_sha256
		FROM deployments d
		JOIN device_deployments dd ON dd.deployment_id = d.id
		WHERE dd.device_id = $1 AND dd.status IN ('pending', 'downloading', 'verifying')
		ORDER BY d.created_at
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
	if err != nil {
		return err
	}
	// The first progress report moves the whole deployment out of "pending".
	_, err = s.pool.Exec(ctx, `
		UPDATE deployments SET status = 'in_progress'
		WHERE id = $1 AND status = 'pending'
	`, deploymentID)
	return err
}

func (s *Store) ListDeployments(ctx context.Context) ([]model.Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+deploymentCols+` FROM deployments ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deployments []model.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, *d)
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
	return scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentCols+` FROM deployments WHERE id = $1`, id))
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

// CheckDeploymentCompletion marks the deployment completed, failed or
// partial_failure once every device it applies to has finished. Devices that
// were skipped (requirements not met) or rolled back do not count; a
// deployment every device skipped is failed. A rolled-back deployment keeps
// that status.
func (s *Store) CheckDeploymentCompletion(ctx context.Context, deploymentID string) error {
	var total, active, running, failed int
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status NOT IN ('skipped', 'rolled_back')),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM device_deployments WHERE deployment_id = $1
	`, deploymentID).Scan(&total, &active, &running, &failed)
	if err != nil {
		return err
	}
	if total == 0 || (active > 0 && running+failed < active) {
		return nil
	}
	var newStatus string
	switch {
	case active == 0:
		newStatus = "failed" // every targeted device was skipped
	case failed == 0:
		newStatus = "completed"
	case running == 0:
		newStatus = "failed"
	default:
		newStatus = "partial_failure"
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE deployments SET status = $2, completed_at = NOW()
		WHERE id = $1 AND status <> 'rolled_back'
	`, deploymentID, newStatus)
	return err
}

// GenerateUUID returns a new random UUID string.
func GenerateUUID() string {
	return uuid.New().String()
}
