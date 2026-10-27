package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	pgstore "github.com/cami-fleet/control-plane/internal/store/postgres"
)

func getDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://cami:cami@localhost:5432/cami?sslmode=disable"
	}
	return dsn
}

func newStore(t *testing.T) *pgstore.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := pgstore.New(ctx, getDSN(t))
	if err != nil {
		t.Skipf("postgres not available, skipping integration test: %v", err)
	}
	return store
}

func TestUpsertAndGetDevice(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	name := "test-device-" + pgstore.GenerateUUID()[:8]
	labels := map[string]string{"location": "barcelona", "type": "jetson"}

	id, err := store.UpsertDevice(ctx, name, labels, "0.1.0")
	if err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty device id")
	}

	device, err := store.GetDevice(ctx, id)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if device.Name != name {
		t.Errorf("expected name %q, got %q", name, device.Name)
	}
	if device.Labels["location"] != "barcelona" {
		t.Errorf("expected label location=barcelona, got %v", device.Labels)
	}
	if string(device.Status) != "online" {
		t.Errorf("expected status online, got %s", device.Status)
	}
}

func TestUpsertDeviceIdempotent(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	name := "idempotent-" + pgstore.GenerateUUID()[:8]
	labels := map[string]string{"env": "test"}

	id1, err := store.UpsertDevice(ctx, name, labels, "0.1.0")
	if err != nil {
		t.Fatalf("first UpsertDevice: %v", err)
	}

	id2, err := store.UpsertDevice(ctx, name, map[string]string{"env": "prod"}, "0.2.0")
	if err != nil {
		t.Fatalf("second UpsertDevice: %v", err)
	}

	if id1 != id2 {
		t.Errorf("expected same id on re-register, got %s vs %s", id1, id2)
	}
}

func TestListDevices(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	devices, err := store.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	// Should return at least an empty slice, not error
	_ = devices
}

func TestFindDevicesByLabels(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	name := "label-test-" + pgstore.GenerateUUID()[:8]
	labels := map[string]string{"region": "eu-west", "tier": "edge"}
	_, err := store.UpsertDevice(ctx, name, labels, "0.1.0")
	if err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	found, err := store.FindDevicesByLabels(ctx, map[string]string{"region": "eu-west"})
	if err != nil {
		t.Fatalf("FindDevicesByLabels: %v", err)
	}

	var matched bool
	for _, d := range found {
		if d.Name == name {
			matched = true
			break
		}
	}
	if !matched {
		t.Error("expected to find device by label selector")
	}

	notFound, err := store.FindDevicesByLabels(ctx, map[string]string{"region": "nonexistent"})
	if err != nil {
		t.Fatalf("FindDevicesByLabels: %v", err)
	}
	for _, d := range notFound {
		if d.Name == name {
			t.Error("device should not match non-matching selector")
		}
	}
}

func TestHeartbeatAndStaleDetection(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	name := "stale-test-" + pgstore.GenerateUUID()[:8]
	id, err := store.UpsertDevice(ctx, name, map[string]string{}, "0.1.0")
	if err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	if err := store.TouchHeartbeat(ctx, id); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}

	// Device just heartbeated — should NOT be marked stale with a large threshold
	stale, err := store.MarkStaleDevicesOffline(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("MarkStaleDevicesOffline: %v", err)
	}
	for _, sid := range stale {
		if sid == id {
			t.Error("freshly heartbeated device should not be stale")
		}
	}
}

func TestCreateDeploymentAndList(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	dep, err := store.CreateDeployment(ctx, "gemma-4-e2b",
		"http://cp:8080/artifacts/gemma.tar.gz",
		"abc123def456",
		map[string]string{"location": "barcelona"},
	)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if dep.ID == "" {
		t.Fatal("expected non-empty deployment id")
	}
	if dep.ModelID != "gemma-4-e2b" {
		t.Errorf("expected model_id gemma-4-e2b, got %s", dep.ModelID)
	}
	if string(dep.Status) != "pending" {
		t.Errorf("expected status pending, got %s", dep.Status)
	}

	deps, err := store.ListDeployments(ctx)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deps) == 0 {
		t.Error("expected at least one deployment")
	}
}

func TestDeploymentCompletionLogic(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	dep, err := store.CreateDeployment(ctx, "completion-test",
		"http://cp/model.tar.gz", "sha256hash",
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	dev1Name := "comp-dev1-" + pgstore.GenerateUUID()[:8]
	dev2Name := "comp-dev2-" + pgstore.GenerateUUID()[:8]
	dev1ID, _ := store.UpsertDevice(ctx, dev1Name, map[string]string{}, "0.1.0")
	dev2ID, _ := store.UpsertDevice(ctx, dev2Name, map[string]string{}, "0.1.0")

	store.CreateDeviceDeployment(ctx, dep.ID, dev1ID)
	store.CreateDeviceDeployment(ctx, dep.ID, dev2ID)

	// Both running -> completed
	store.UpdateDeviceDeploymentStatus(ctx, dep.ID, dev1ID, "running", "")
	store.UpdateDeviceDeploymentStatus(ctx, dep.ID, dev2ID, "running", "")
	store.CheckDeploymentCompletion(ctx, dep.ID)

	got, err := store.GetDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if string(got.Status) != "completed" {
		t.Errorf("expected completed, got %s", got.Status)
	}
}

func TestDeploymentPartialFailure(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	dep, err := store.CreateDeployment(ctx, "partial-test",
		"http://cp/model.tar.gz", "sha256hash",
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	dev1Name := "pf-dev1-" + pgstore.GenerateUUID()[:8]
	dev2Name := "pf-dev2-" + pgstore.GenerateUUID()[:8]
	dev1ID, _ := store.UpsertDevice(ctx, dev1Name, map[string]string{}, "0.1.0")
	dev2ID, _ := store.UpsertDevice(ctx, dev2Name, map[string]string{}, "0.1.0")

	store.CreateDeviceDeployment(ctx, dep.ID, dev1ID)
	store.CreateDeviceDeployment(ctx, dep.ID, dev2ID)

	// One running, one failed -> partial_failure
	store.UpdateDeviceDeploymentStatus(ctx, dep.ID, dev1ID, "running", "")
	store.UpdateDeviceDeploymentStatus(ctx, dep.ID, dev2ID, "failed", "download error")
	store.CheckDeploymentCompletion(ctx, dep.ID)

	got, err := store.GetDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if string(got.Status) != "partial_failure" {
		t.Errorf("expected partial_failure, got %s", got.Status)
	}
}

func TestDeviceAssignmentsNewestFirstWithReason(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	devID, err := store.UpsertDevice(ctx, "asg-dev-"+pgstore.GenerateUUID()[:8], map[string]string{}, "0.1.0")
	if err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	older, _ := store.CreateDeployment(ctx, "asg-older", "http://cp/a", "sha", map[string]string{})
	time.Sleep(10 * time.Millisecond)
	newer, _ := store.CreateDeployment(ctx, "asg-newer", "http://cp/b", "sha", map[string]string{})

	if _, err := store.TargetDevice(ctx, older.ID, devID, "pending", ""); err != nil {
		t.Fatalf("TargetDevice: %v", err)
	}
	if _, err := store.TargetDevice(ctx, newer.ID, devID, "skipped", "needs 2048 MB RAM, device has 1 MB"); err != nil {
		t.Fatalf("TargetDevice: %v", err)
	}

	got, err := store.DeviceAssignments(ctx, devID, 10)
	if err != nil {
		t.Fatalf("DeviceAssignments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(got))
	}
	if got[0].ModelID != "asg-newer" || got[0].Status != "skipped" {
		t.Errorf("expected newest first and skipped, got %s/%s", got[0].ModelID, got[0].Status)
	}
	if got[0].ErrorMsg == nil || *got[0].ErrorMsg != "needs 2048 MB RAM, device has 1 MB" {
		t.Errorf("expected skip reason, got %v", got[0].ErrorMsg)
	}
	if got[1].ModelID != "asg-older" || got[1].Status != "pending" {
		t.Errorf("expected older pending second, got %s/%s", got[1].ModelID, got[1].Status)
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	_, err := store.GetDevice(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("expected error for nonexistent device")
	}
}
