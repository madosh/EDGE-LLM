package clickhouse_test

import (
	"context"
	"os"
	"testing"
	"time"

	chstore "github.com/cami-fleet/control-plane/internal/store/clickhouse"
	"github.com/cami-fleet/control-plane/internal/model"
)

func getDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CLICKHOUSE_DSN")
	if dsn == "" {
		dsn = "clickhouse://cami:cami@localhost:9000/cami"
	}
	return dsn
}

func newStore(t *testing.T) *chstore.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping clickhouse integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := chstore.New(ctx, getDSN(t))
	if err != nil {
		t.Skipf("clickhouse not available, skipping integration test: %v", err)
	}
	return store
}

func TestInsertAndQueryTelemetry(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	deviceID := "test-device-ch-001"
	row := &model.TelemetryRow{
		DeviceID: deviceID,
		ModelID:  "gemma-4-e2b",
		TPS:      28.5,
		TTFTMs:   120.0,
		MemMB:    3800.0,
		Status:   "running",
		Ts:       time.Now(),
	}

	if err := store.InsertTelemetry(ctx, row); err != nil {
		t.Fatalf("InsertTelemetry: %v", err)
	}

	points, err := store.QueryTelemetry(ctx, deviceID, 10)
	if err != nil {
		t.Fatalf("QueryTelemetry: %v", err)
	}

	if len(points) == 0 {
		t.Fatal("expected at least one telemetry point")
	}

	latest := points[0]
	if latest.DeviceID != deviceID {
		t.Errorf("expected device_id %q, got %q", deviceID, latest.DeviceID)
	}
	if latest.ModelID != "gemma-4-e2b" {
		t.Errorf("expected model_id gemma-4-e2b, got %s", latest.ModelID)
	}
	if latest.TPS < 28.0 || latest.TPS > 29.0 {
		t.Errorf("expected tps ~28.5, got %f", latest.TPS)
	}
}

func TestQueryTelemetryEmpty(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	points, err := store.QueryTelemetry(ctx, "nonexistent-device-xyz", 10)
	if err != nil {
		t.Fatalf("QueryTelemetry: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected empty results for nonexistent device, got %d", len(points))
	}
}

func TestQueryTelemetryLimit(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	deviceID := "limit-test-device"
	for i := 0; i < 5; i++ {
		row := &model.TelemetryRow{
			DeviceID: deviceID,
			ModelID:  "test-model",
			TPS:      float32(25 + i),
			TTFTMs:   100.0,
			MemMB:    3000.0,
			Status:   "running",
			Ts:       time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := store.InsertTelemetry(ctx, row); err != nil {
			t.Fatalf("InsertTelemetry[%d]: %v", i, err)
		}
	}

	points, err := store.QueryTelemetry(ctx, deviceID, 3)
	if err != nil {
		t.Fatalf("QueryTelemetry: %v", err)
	}
	if len(points) > 3 {
		t.Errorf("expected at most 3 points, got %d", len(points))
	}
}

func TestQueryTelemetryOrderDescending(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	deviceID := "order-test-device"
	for i := 0; i < 3; i++ {
		row := &model.TelemetryRow{
			DeviceID: deviceID,
			ModelID:  "test-model",
			TPS:      float32(i + 1),
			TTFTMs:   100.0,
			MemMB:    3000.0,
			Status:   "running",
			Ts:       time.Now().Add(time.Duration(i) * time.Second),
		}
		store.InsertTelemetry(ctx, row)
	}

	points, err := store.QueryTelemetry(ctx, deviceID, 10)
	if err != nil {
		t.Fatalf("QueryTelemetry: %v", err)
	}
	if len(points) >= 2 {
		if points[0].Ts.Before(points[1].Ts) {
			t.Error("expected descending order by timestamp")
		}
	}
}
