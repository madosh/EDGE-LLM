package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/cami-fleet/control-plane/internal/model"
)

type Store struct {
	db *sql.DB
}

type TelemetryPoint struct {
	DeviceID string    `json:"device_id"`
	ModelID  string    `json:"model_id"`
	TPS      float64   `json:"tps"`
	TTFTMs   float64   `json:"ttft_ms"`
	MemMB    float64   `json:"mem_mb"`
	Status   string    `json:"status"`
	Ts       time.Time `json:"ts"`
}

func New(ctx context.Context, dsn string) (*Store, error) {
	opts, err := ch.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse parse dsn: %w", err)
	}
	opts.DialTimeout = 30 * time.Second

	var db *sql.DB
	for i := 0; i < 20; i++ {
		db = ch.OpenDB(opts)
		if pingErr := db.PingContext(ctx); pingErr == nil {
			break
		}
		db.Close()
		db = nil
		time.Sleep(3 * time.Second)
	}
	if db == nil {
		return nil, fmt.Errorf("clickhouse: failed to connect after retries")
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("clickhouse migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS telemetry (
			device_id String,
			model_id  String,
			tps       Float32,
			ttft_ms   Float32,
			mem_mb    Float32,
			status    String,
			ts        DateTime
		) ENGINE = MergeTree()
		ORDER BY (device_id, ts)
		TTL ts + INTERVAL 7 DAY
	`)
	return err
}

func (s *Store) InsertTelemetry(ctx context.Context, r *model.TelemetryRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO telemetry (device_id, model_id, tps, ttft_ms, mem_mb, status, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.DeviceID, r.ModelID, r.TPS, r.TTFTMs, r.MemMB, r.Status, r.Ts)
	return err
}

func (s *Store) QueryTelemetry(ctx context.Context, deviceID string, limit int) ([]TelemetryPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_id, model_id, tps, ttft_ms, mem_mb, status, ts
		FROM telemetry
		WHERE device_id = ?
		ORDER BY ts DESC
		LIMIT ?
	`, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []TelemetryPoint
	for rows.Next() {
		var p TelemetryPoint
		if err := rows.Scan(&p.DeviceID, &p.ModelID, &p.TPS, &p.TTFTMs, &p.MemMB, &p.Status, &p.Ts); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
