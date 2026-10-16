package model

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Requirements are what a model needs from a device. Zero values mean "no
// requirement". A device that matches a deployment's labels but not its
// requirements is recorded as skipped, with the reason, instead of failing
// at load time.
type Requirements struct {
	// MinMemMB is the device RAM the model needs, in MiB.
	MinMemMB int64 `json:"min_mem_mb,omitempty"`
	// Accelerator is "cpu", "gpu" or "npu"; the device must report it.
	Accelerator string `json:"accelerator,omitempty"`
	// Arch lists accepted CPU architectures (e.g. "aarch64", "x86_64").
	Arch []string `json:"arch,omitempty"`
}

// Capabilities are what a device reports about itself at registration.
type Capabilities struct {
	Arch         string
	MemTotalMB   int64
	Accelerators []string
}

// Validate rejects requirement values the control plane cannot check.
func (r Requirements) Validate() error {
	if r.MinMemMB < 0 {
		return fmt.Errorf("min_mem_mb must not be negative")
	}
	switch r.Accelerator {
	case "", "cpu", "gpu", "npu":
	default:
		return fmt.Errorf("accelerator must be cpu, gpu or npu, got %q", r.Accelerator)
	}
	for _, a := range r.Arch {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("arch entries must not be empty")
		}
	}
	return nil
}

// Check reports whether a device can run the model, and if not, why.
// A device that reported 0 MB (an older agent) is not held to MinMemMB.
func (r Requirements) Check(c Capabilities) (ok bool, reason string) {
	if r.MinMemMB > 0 && c.MemTotalMB > 0 && c.MemTotalMB < r.MinMemMB {
		return false, fmt.Sprintf("needs %d MB RAM, device has %d MB", r.MinMemMB, c.MemTotalMB)
	}
	if r.Accelerator != "" && !contains(c.Accelerators, r.Accelerator) {
		have := "none reported"
		if len(c.Accelerators) > 0 {
			have = strings.Join(c.Accelerators, ", ")
		}
		return false, fmt.Sprintf("needs %s accelerator, device has %s", r.Accelerator, have)
	}
	if len(r.Arch) > 0 && !contains(r.Arch, c.Arch) {
		return false, fmt.Sprintf("needs arch %s, device is %s", strings.Join(r.Arch, " or "), c.Arch)
	}
	return true, ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// RolloutBucket places a device in [0, 100) for a deployment. It is stable —
// the same device always lands in the same bucket for the same deployment —
// so raising the percentage only ever adds devices, never swaps them. Mixing
// in the deployment ID means a different set of devices goes first each time.
func RolloutBucket(deploymentID, deviceID string) int {
	h := fnv.New32a()
	h.Write([]byte(deploymentID))
	h.Write([]byte{0})
	h.Write([]byte(deviceID))
	return int(h.Sum32() % 100)
}

// InRollout reports whether a device is included at the given percentage.
func InRollout(deploymentID, deviceID string, percent int) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}
	return RolloutBucket(deploymentID, deviceID) < percent
}
