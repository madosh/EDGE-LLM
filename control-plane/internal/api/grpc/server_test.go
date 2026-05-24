package grpc_test

import (
	"sync"
	"testing"
	"time"

	pb "github.com/cami-fleet/control-plane/gen"
	grpcapi "github.com/cami-fleet/control-plane/internal/api/grpc"
)

func TestPushDeploymentFanout(t *testing.T) {
	srv := grpcapi.NewServer(nil, nil, nil)

	instr := &pb.DeploymentInstruction{
		DeploymentId:   "dep-1",
		ModelId:        "gemma-4-e2b",
		ArtifactUrl:    "http://cp/artifacts/model.tar.gz",
		ArtifactSha256: "abc123",
	}

	// No watchers — should not panic
	srv.PushDeployment("device-1", instr)
}

func TestPushDeploymentReachesWatcher(t *testing.T) {
	srv := grpcapi.NewServer(nil, nil, nil)

	deviceID := "test-device-watcher"
	ch := srv.RegisterWatcher(deviceID)

	instr := &pb.DeploymentInstruction{
		DeploymentId: "dep-push-test",
		ModelId:      "gemma",
	}
	srv.PushDeployment(deviceID, instr)

	select {
	case got := <-ch:
		if got.DeploymentId != "dep-push-test" {
			t.Errorf("expected deployment_id dep-push-test, got %s", got.DeploymentId)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deployment instruction")
	}
}

func TestPushDeploymentMultipleWatchers(t *testing.T) {
	srv := grpcapi.NewServer(nil, nil, nil)

	deviceID := "multi-watcher-device"
	ch1 := srv.RegisterWatcher(deviceID)
	ch2 := srv.RegisterWatcher(deviceID)

	instr := &pb.DeploymentInstruction{
		DeploymentId: "dep-multi",
		ModelId:      "test-model",
	}
	srv.PushDeployment(deviceID, instr)

	var wg sync.WaitGroup
	received := make([]string, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		select {
		case got := <-ch1:
			received[0] = got.DeploymentId
		case <-time.After(time.Second):
		}
	}()
	go func() {
		defer wg.Done()
		select {
		case got := <-ch2:
			received[1] = got.DeploymentId
		case <-time.After(time.Second):
		}
	}()
	wg.Wait()

	for i, r := range received {
		if r != "dep-multi" {
			t.Errorf("watcher %d: expected dep-multi, got %q", i, r)
		}
	}
}

func TestPushToWrongDeviceDoesNotDeliver(t *testing.T) {
	srv := grpcapi.NewServer(nil, nil, nil)

	ch := srv.RegisterWatcher("device-A")

	srv.PushDeployment("device-B", &pb.DeploymentInstruction{
		DeploymentId: "dep-wrong",
	})

	select {
	case got := <-ch:
		t.Errorf("should not receive instruction for different device, got %s", got.DeploymentId)
	case <-time.After(100 * time.Millisecond):
		// Expected: no delivery
	}
}

func TestNewServerNotNil(t *testing.T) {
	srv := grpcapi.NewServer(nil, nil, nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}
