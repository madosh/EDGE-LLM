package model

import "time"

type DeploymentStatus string

const (
	DeploymentPending        DeploymentStatus = "pending"
	DeploymentInProgress     DeploymentStatus = "in_progress"
	DeploymentCompleted      DeploymentStatus = "completed"
	DeploymentFailed         DeploymentStatus = "failed"
	DeploymentPartialFailure DeploymentStatus = "partial_failure"
	DeploymentRolledBack     DeploymentStatus = "rolled_back"
)

type DeviceDeploymentStatus string

const (
	DDPending     DeviceDeploymentStatus = "pending"
	DDDownloading DeviceDeploymentStatus = "downloading"
	DDVerifying   DeviceDeploymentStatus = "verifying"
	DDRunning     DeviceDeploymentStatus = "running"
	DDFailed      DeviceDeploymentStatus = "failed"
	// Set by the control plane, never by a device:
	DDSkipped    DeviceDeploymentStatus = "skipped"     // device does not meet the requirements
	DDRolledBack DeviceDeploymentStatus = "rolled_back" // the deployment was rolled back
)

// IsAckStatus reports whether s is a status a device may report for its own
// deployment. "pending" is set by the control plane only.
func IsAckStatus(s string) bool {
	switch DeviceDeploymentStatus(s) {
	case DDDownloading, DDVerifying, DDRunning, DDFailed:
		return true
	}
	return false
}

type Deployment struct {
	ID             string             `json:"id"`
	ModelID        string             `json:"model_id"`
	ArtifactURL    string             `json:"artifact_url"`
	ArtifactSHA256 string             `json:"artifact_sha256"`
	TagSelector    map[string]string  `json:"tag_selector"`
	RolloutPercent int                `json:"rollout_percent"`
	Requirements   Requirements       `json:"requirements"`
	Status         DeploymentStatus   `json:"status"`
	CreatedAt      time.Time          `json:"created_at"`
	CompletedAt    *time.Time         `json:"completed_at"`
	Devices        []DeviceDeployment `json:"devices,omitempty"`
}

type DeviceDeployment struct {
	DeploymentID string                 `json:"deployment_id"`
	DeviceID     string                 `json:"device_id"`
	DeviceName   string                 `json:"device_name,omitempty"`
	Status       DeviceDeploymentStatus `json:"status"`
	ErrorMsg     *string                `json:"error_msg"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

type ArtifactInfo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
