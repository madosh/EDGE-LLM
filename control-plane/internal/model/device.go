package model

import "time"

type DeviceStatus string

const (
	StatusOnline  DeviceStatus = "online"
	StatusOffline DeviceStatus = "offline"
)

type Device struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Labels         map[string]string `json:"labels"`
	Status         DeviceStatus      `json:"status"`
	LastSeenAt     time.Time         `json:"last_seen_at"`
	CurrentModelID *string           `json:"current_model_id"`
	AgentVersion   string            `json:"agent_version"`
	Arch           string            `json:"arch"`
	OS             string            `json:"os"`
	MemTotalMB     int64             `json:"mem_total_mb"`
	Accelerators   []string          `json:"accelerators"`
	CreatedAt      time.Time         `json:"created_at"`
}

// Capabilities returns what the device reported about its hardware.
func (d Device) Capabilities() Capabilities {
	return Capabilities{Arch: d.Arch, MemTotalMB: d.MemTotalMB, Accelerators: d.Accelerators}
}
