package model_test

import (
	"testing"

	"github.com/cami-fleet/control-plane/internal/model"
)

func TestDeploymentStatusConstants(t *testing.T) {
	statuses := []model.DeploymentStatus{
		model.DeploymentPending,
		model.DeploymentInProgress,
		model.DeploymentCompleted,
		model.DeploymentFailed,
	}
	seen := make(map[model.DeploymentStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
		if string(s) == "" {
			t.Error("empty status string")
		}
	}
}

func TestDeviceDeploymentStatusConstants(t *testing.T) {
	statuses := []model.DeviceDeploymentStatus{
		model.DDPending,
		model.DDDownloading,
		model.DDVerifying,
		model.DDRunning,
		model.DDFailed,
	}
	seen := make(map[model.DeviceDeploymentStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
	}
}
