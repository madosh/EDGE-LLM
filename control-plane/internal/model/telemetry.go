package model

import "time"

type TelemetryRow struct {
	DeviceID string
	ModelID  string
	TPS      float32
	TTFTMs   float32
	MemMB    float32
	Status   string
	Ts       time.Time
}
