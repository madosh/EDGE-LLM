// Package fleet decides which devices a deployment applies to.
//
// A deployment is a desired state: "devices matching this selector should run
// this model". It applies to a device when the device's labels match, the
// device falls inside the rollout percentage, and the device meets the
// model's requirements. Creating a deployment, promoting its rollout, and a
// device (re)connecting all go through Target, so a device that joins the
// fleet later gets the same answer as one that was there from the start.
package fleet

import (
	"context"

	pb "github.com/cami-fleet/control-plane/gen"
	"github.com/cami-fleet/control-plane/internal/model"
)

// Store is the slice of the Postgres store targeting needs.
type Store interface {
	TargetDevice(ctx context.Context, deploymentID, deviceID string, status model.DeviceDeploymentStatus, reason string) (bool, error)
	RetrySkipped(ctx context.Context, deploymentID, deviceID string) (bool, error)
}

// Pusher delivers an instruction to a connected device's watch stream.
type Pusher interface {
	PushDeployment(deviceID string, instr *pb.DeploymentInstruction)
}

// Outcome is what Target did for one device.
type Outcome string

const (
	// Targeted: the device is now pending for this deployment.
	Targeted Outcome = "targeted"
	// Skipped: the device matched but does not meet the requirements.
	Skipped Outcome = "skipped"
	// HeldBack: the device is outside the current rollout percentage.
	HeldBack Outcome = "held_back"
	// Existing: the device already had this deployment; nothing changed.
	Existing Outcome = "existing"
)

// Instruction is the message a device receives for a deployment.
func Instruction(d *model.Deployment) *pb.DeploymentInstruction {
	return &pb.DeploymentInstruction{
		DeploymentId:   d.ID,
		ModelId:        d.ModelID,
		ArtifactUrl:    d.ArtifactURL,
		ArtifactSha256: d.ArtifactSHA256,
	}
}

// Target applies a deployment to one device whose labels already match.
// When push is true and the device became pending, the instruction is sent
// to its open watch stream right away; otherwise it goes out the next time
// the device connects. The returned reason explains a skip.
func Target(ctx context.Context, st Store, p Pusher, d *model.Deployment, dev model.Device, push bool) (Outcome, string, error) {
	if !model.InRollout(d.ID, dev.ID, d.RolloutPercent) {
		return HeldBack, "", nil
	}
	if ok, reason := d.Requirements.Check(dev.Capabilities()); !ok {
		inserted, err := st.TargetDevice(ctx, d.ID, dev.ID, model.DDSkipped, reason)
		if err != nil {
			return "", "", err
		}
		if !inserted {
			return Existing, reason, nil
		}
		return Skipped, reason, nil
	}

	inserted, err := st.TargetDevice(ctx, d.ID, dev.ID, model.DDPending, "")
	if err != nil {
		return "", "", err
	}
	if !inserted {
		// Already targeted. If it was skipped before and now qualifies (an
		// upgraded device, or an older agent that reported no hardware),
		// make it pending.
		retried, err := st.RetrySkipped(ctx, d.ID, dev.ID)
		if err != nil || !retried {
			return Existing, "", err
		}
	}
	if push && p != nil {
		p.PushDeployment(dev.ID, Instruction(d))
	}
	return Targeted, "", nil
}

// Desired returns the deployment a device should run: the newest matching
// deployment (candidates come newest first) whose rollout includes the device.
func Desired(candidates []model.Deployment, deviceID string) *model.Deployment {
	for i := range candidates {
		if model.InRollout(candidates[i].ID, deviceID, candidates[i].RolloutPercent) {
			return &candidates[i]
		}
	}
	return nil
}

// Summary counts outcomes for an API response.
type Summary struct {
	Targeted []string          `json:"targeted"`
	Skipped  map[string]string `json:"skipped"` // device ID -> reason
	HeldBack int               `json:"held_back"`
}

func NewSummary() *Summary {
	return &Summary{Targeted: []string{}, Skipped: map[string]string{}}
}

func (s *Summary) Add(deviceID string, o Outcome, reason string) {
	switch o {
	case Targeted:
		s.Targeted = append(s.Targeted, deviceID)
	case Skipped:
		s.Skipped[deviceID] = reason
	case HeldBack:
		s.HeldBack++
	}
}
