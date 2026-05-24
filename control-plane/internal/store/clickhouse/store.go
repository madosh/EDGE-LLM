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

// FleetSummary holds aggregated telemetry across all devices.
type FleetSummary struct {
	DeviceCount int     `json:"device_count"`
	AvgTPS      float64 `json:"avg_tps"`
	P50TPS      float64 `json:"p50_tps"`
	P95TPS      float64 `json:"p95_tps"`
	AvgTTFTMs   float64 `json:"avg_ttft_ms"`
	P50TTFTMs   float64 `json:"p50_ttft_ms"`
	P95TTFTMs   float64 `json:"p95_ttft_ms"`
	AvgMemMB    float64 `json:"avg_mem_mb"`
	TotalMemMB  float64 `json:"total_mem_mb"`
}

// QueryFleetSummary returns aggregated metrics across all devices within a time window.
func (s *Store) QueryFleetSummary(ctx context.Context, window time.Duration) (*FleetSummary, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			uniqExact(device_id) AS device_count,
			avg(tps) AS avg_tps,
			quantile(0.5)(tps) AS p50_tps,
			quantile(0.95)(tps) AS p95_tps,
			avg(ttft_ms) AS avg_ttft_ms,
			quantile(0.5)(ttft_ms) AS p50_ttft_ms,
			quantile(0.95)(ttft_ms) AS p95_ttft_ms,
			avg(mem_mb) AS avg_mem_mb,
			sum(mem_mb) / greatest(uniqExact(device_id), 1) AS total_mem_mb
		FROM telemetry
		WHERE ts >= now() - toIntervalSecond(?)
		  AND status = 'running'
	`, int(window.Seconds()))

	var fs FleetSummary
	err := row.Scan(
		&fs.DeviceCount, &fs.AvgTPS, &fs.P50TPS, &fs.P95TPS,
		&fs.AvgTTFTMs, &fs.P50TTFTMs, &fs.P95TTFTMs,
		&fs.AvgMemMB, &fs.TotalMemMB,
	)
	if err != nil {
		return nil, err
	}
	return &fs, nil
}

// DeviceMetric holds per-device latest metrics for fleet views.
type DeviceMetric struct {
	DeviceID string  `json:"device_id"`
	ModelID  string  `json:"model_id"`
	TPS      float64 `json:"tps"`
	TTFTMs   float64 `json:"ttft_ms"`
	MemMB    float64 `json:"mem_mb"`
	Status   string  `json:"status"`
}

// QueryFleetLatest returns the most recent telemetry point per device.
func (s *Store) QueryFleetLatest(ctx context.Context) ([]DeviceMetric, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_id, model_id, tps, ttft_ms, mem_mb, status
		FROM telemetry
		WHERE (device_id, ts) IN (
			SELECT device_id, max(ts)
			FROM telemetry
			WHERE ts >= now() - toIntervalSecond(300)
			GROUP BY device_id
		)
		ORDER BY tps ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []DeviceMetric
	for rows.Next() {
		var m DeviceMetric
		if err := rows.Scan(&m.DeviceID, &m.ModelID, &m.TPS, &m.TTFTMs, &m.MemMB, &m.Status); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// QuerySlowDevices returns devices whose latest TPS is below the threshold.
func (s *Store) QuerySlowDevices(ctx context.Context, threshold float64) ([]DeviceMetric, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT device_id, model_id, tps, ttft_ms, mem_mb, status
		FROM telemetry
		WHERE (device_id, ts) IN (
			SELECT device_id, max(ts)
			FROM telemetry
			WHERE ts >= now() - toIntervalSecond(300)
			GROUP BY device_id
		)
		AND tps < ?
		ORDER BY tps ASC
	`, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []DeviceMetric
	for rows.Next() {
		var m DeviceMetric
		if err := rows.Scan(&m.DeviceID, &m.ModelID, &m.TPS, &m.TTFTMs, &m.MemMB, &m.Status); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// FleetTimeSeries holds a single time-bucket for fleet charts.
type FleetTimeSeries struct {
	Ts     time.Time `json:"ts"`
	AvgTPS float64   `json:"avg_tps"`
	AvgTTF float64   `json:"avg_ttft_ms"`
	AvgMem float64   `json:"avg_mem_mb"`
	Count  int       `json:"device_count"`
}

// QueryFleetTimeSeries returns bucketed averages for fleet-wide charting.
func (s *Store) QueryFleetTimeSeries(ctx context.Context, window time.Duration, bucketSec int) ([]FleetTimeSeries, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			toStartOfInterval(ts, toIntervalSecond(?)) AS bucket,
			avg(tps),
			avg(ttft_ms),
			avg(mem_mb),
			uniqExact(device_id)
		FROM telemetry
		WHERE ts >= now() - toIntervalSecond(?)
		  AND status = 'running'
		GROUP BY bucket
		ORDER BY bucket ASC
	`, bucketSec, int(window.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var series []FleetTimeSeries
	for rows.Next() {
		var p FleetTimeSeries
		if err := rows.Scan(&p.Ts, &p.AvgTPS, &p.AvgTTF, &p.AvgMem, &p.Count); err != nil {
			return nil, err
		}
		series = append(series, p)
	}
	return series, rows.Err()
}
