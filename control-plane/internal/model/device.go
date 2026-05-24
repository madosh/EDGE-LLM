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
	CreatedAt      time.Time         `json:"created_at"`
}
