package model_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cami-fleet/control-plane/internal/model"
)

func TestRequirementsCheck(t *testing.T) {
	pi := model.Capabilities{Arch: "aarch64", MemTotalMB: 8192, Accelerators: []string{"cpu", "gpu"}}

	cases := []struct {
		name   string
		req    model.Requirements
		ok     bool
		reason string
	}{
		{"no requirements", model.Requirements{}, true, ""},
		{"enough memory", model.Requirements{MinMemMB: 4096}, true, ""},
		{"not enough memory", model.Requirements{MinMemMB: 16384}, false, "needs 16384 MB RAM, device has 8192 MB"},
		{"has gpu", model.Requirements{Accelerator: "gpu"}, true, ""},
		{"no npu", model.Requirements{Accelerator: "npu"}, false, "needs npu accelerator, device has cpu, gpu"},
		{"arch accepted", model.Requirements{Arch: []string{"x86_64", "aarch64"}}, true, ""},
		{"arch rejected", model.Requirements{Arch: []string{"x86_64"}}, false, "needs arch x86_64, device is aarch64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := tc.req.Check(pi)
			if ok != tc.ok || reason != tc.reason {
				t.Fatalf("Check = (%v, %q), want (%v, %q)", ok, reason, tc.ok, tc.reason)
			}
		})
	}
}

func TestRequirementsCheckOlderAgentWithoutMemory(t *testing.T) {
	// Agents before capability reporting send 0 MB; don't skip them on memory.
	ok, _ := model.Requirements{MinMemMB: 4096}.Check(model.Capabilities{Arch: "x86_64"})
	if !ok {
		t.Fatal("a device that reported no memory figure should not be skipped on memory")
	}
	// But an accelerator requirement still needs the accelerator to be reported.
	ok, reason := model.Requirements{Accelerator: "gpu"}.Check(model.Capabilities{})
	if ok || !strings.Contains(reason, "none reported") {
		t.Fatalf("expected skip with 'none reported', got (%v, %q)", ok, reason)
	}
}

func TestRequirementsValidate(t *testing.T) {
	for _, bad := range []model.Requirements{
		{MinMemMB: -1},
		{Accelerator: "tpu"},
		{Arch: []string{" "}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("expected %+v to be invalid", bad)
		}
	}
	if err := (model.Requirements{MinMemMB: 2048, Accelerator: "npu", Arch: []string{"aarch64"}}).Validate(); err != nil {
		t.Errorf("valid requirements rejected: %v", err)
	}
}

func TestRolloutIsStableAndMonotonic(t *testing.T) {
	const dep = "3f2a9c1e-5b7d-4e8a-9c21-7d4e5f6a8b90"
	devices := make([]string, 1000)
	for i := range devices {
		devices[i] = fmt.Sprintf("device-%04d", i)
	}

	prev := map[string]bool{}
	for _, pct := range []int{0, 5, 10, 25, 50, 100} {
		included := map[string]bool{}
		for _, d := range devices {
			if model.InRollout(dep, d, pct) {
				included[d] = true
			}
		}
		// Raising the percentage only adds devices.
		for d := range prev {
			if !included[d] {
				t.Fatalf("device %s dropped out when rollout went up to %d%%", d, pct)
			}
		}
		// Roughly the requested share of the fleet (±4 points over 1000 devices).
		got := len(included) * 100 / len(devices)
		if got < pct-4 || got > pct+4 {
			t.Errorf("at %d%% the rollout included %d%% of devices", pct, got)
		}
		prev = included
	}
	if len(prev) != len(devices) {
		t.Fatalf("100%% must include every device, got %d", len(prev))
	}
}

func TestRolloutDiffersPerDeployment(t *testing.T) {
	// The canary set should not be the same devices every time.
	same := 0
	for i := 0; i < 200; i++ {
		d := fmt.Sprintf("device-%d", i)
		if model.InRollout("deployment-a", d, 10) == model.InRollout("deployment-b", d, 10) {
			same++
		}
	}
	if same == 200 {
		t.Fatal("two deployments picked exactly the same canary devices")
	}
}
