package fleet

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/cami-fleet/control-plane/gen"
	"github.com/cami-fleet/control-plane/internal/model"
)

type fakeStore struct {
	rows map[string]model.DeviceDeploymentStatus // deployment/device -> status
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]model.DeviceDeploymentStatus{}} }

func (f *fakeStore) TargetDevice(_ context.Context, dep, dev string, st model.DeviceDeploymentStatus, _ string) (bool, error) {
	k := dep + "/" + dev
	if _, ok := f.rows[k]; ok {
		return false, nil
	}
	f.rows[k] = st
	return true, nil
}

func (f *fakeStore) RetrySkipped(_ context.Context, dep, dev string) (bool, error) {
	k := dep + "/" + dev
	if f.rows[k] == model.DDSkipped {
		f.rows[k] = model.DDPending
		return true, nil
	}
	return false, nil
}

type fakePusher struct{ sent map[string][]string }

func (p *fakePusher) PushDeployment(dev string, instr *pb.DeploymentInstruction) {
	if p.sent == nil {
		p.sent = map[string][]string{}
	}
	p.sent[dev] = append(p.sent[dev], instr.DeploymentId)
}

func pi(id string, memMB int64) model.Device {
	return model.Device{ID: id, Arch: "aarch64", MemTotalMB: memMB, Accelerators: []string{"cpu"}}
}

func TestTargetPendingAndPushed(t *testing.T) {
	st, p := newFakeStore(), &fakePusher{}
	d := &model.Deployment{ID: "dep-1", ModelID: "gemma", RolloutPercent: 100}

	o, _, err := Target(context.Background(), st, p, d, pi("dev-1", 8192), true)
	if err != nil || o != Targeted {
		t.Fatalf("got (%v, %v), want targeted", o, err)
	}
	if st.rows["dep-1/dev-1"] != model.DDPending {
		t.Fatalf("row status %q, want pending", st.rows["dep-1/dev-1"])
	}
	if len(p.sent["dev-1"]) != 1 {
		t.Fatalf("instruction pushed %d times, want 1", len(p.sent["dev-1"]))
	}

	// Targeting again (e.g. on reconnect) changes nothing and sends nothing.
	o, _, _ = Target(context.Background(), st, p, d, pi("dev-1", 8192), true)
	if o != Existing || len(p.sent["dev-1"]) != 1 {
		t.Fatalf("second Target: outcome %v, pushes %d", o, len(p.sent["dev-1"]))
	}
}

func TestTargetSkipsDeviceThatLacksRequirements(t *testing.T) {
	st, p := newFakeStore(), &fakePusher{}
	d := &model.Deployment{ID: "dep-1", RolloutPercent: 100, Requirements: model.Requirements{MinMemMB: 8192}}

	o, reason, err := Target(context.Background(), st, p, d, pi("small-pi", 4096), true)
	if err != nil || o != Skipped || reason == "" {
		t.Fatalf("got (%v, %q, %v), want skipped with a reason", o, reason, err)
	}
	if len(p.sent) != 0 {
		t.Fatal("a skipped device must not be sent the instruction")
	}

	// The device is upgraded (or an old agent now reports its RAM): it becomes pending.
	o, _, _ = Target(context.Background(), st, p, d, pi("small-pi", 16384), true)
	if o != Targeted || st.rows["dep-1/small-pi"] != model.DDPending || len(p.sent["small-pi"]) != 1 {
		t.Fatalf("after upgrade: outcome %v, status %q, pushes %d", o, st.rows["dep-1/small-pi"], len(p.sent["small-pi"]))
	}
}

func TestTargetHoldsBackDevicesOutsideRollout(t *testing.T) {
	st, p := newFakeStore(), &fakePusher{}
	d := &model.Deployment{ID: "canary-dep", RolloutPercent: 10}

	sum := NewSummary()
	for i := 0; i < 200; i++ {
		dev := pi(fmt.Sprintf("dev-%d", i), 8192)
		o, r, err := Target(context.Background(), st, p, d, dev, true)
		if err != nil {
			t.Fatal(err)
		}
		sum.Add(dev.ID, o, r)
	}
	if len(sum.Targeted)+sum.HeldBack != 200 {
		t.Fatalf("targeted %d + held back %d != 200", len(sum.Targeted), sum.HeldBack)
	}
	if len(sum.Targeted) < 8 || len(sum.Targeted) > 40 {
		t.Fatalf("10%% canary targeted %d of 200 devices", len(sum.Targeted))
	}
	if len(st.rows) != len(sum.Targeted) {
		t.Fatal("held-back devices must not get a row")
	}
}

func TestDesiredPicksNewestDeploymentThatIncludesDevice(t *testing.T) {
	// Newest first: a 0% canary that excludes everyone, then the stable one.
	cands := []model.Deployment{
		{ID: "canary", RolloutPercent: 0},
		{ID: "stable", RolloutPercent: 100},
		{ID: "older", RolloutPercent: 100},
	}
	got := Desired(cands, "dev-1")
	if got == nil || got.ID != "stable" {
		t.Fatalf("Desired = %v, want stable", got)
	}
	if Desired(nil, "dev-1") != nil {
		t.Fatal("no candidates must mean no desired deployment")
	}
}
