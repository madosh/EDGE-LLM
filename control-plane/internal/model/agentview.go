package model

// AgentView is one fleet member as the status display draws it: whether it
// is up, the hardware it has, and what its newest deployment asked of it.
type AgentView struct {
	ID           string       `json:"-"`
	Name         string       `json:"name"`
	Status       DeviceStatus `json:"status"`
	Arch         string       `json:"arch"`
	MemTotalMB   int64        `json:"mem_total_mb"`
	Accelerators []string     `json:"accelerators"`
	CurrentModel string       `json:"current_model"`
	Asked        *AgentAsk    `json:"asked,omitempty"`
	Live         *AgentLive   `json:"live,omitempty"`
}

// AgentLive is the agent's latest inference sample from the last few
// minutes. Source is "probe" when measured on a real runtime and "stub" when
// the numbers are simulated, so the display never passes one off as the other.
type AgentLive struct {
	TPS    float64 `json:"tps"`
	TTFTMs float64 `json:"ttft_ms"`
	Source string  `json:"source"`
}

// AgentAsk is the newest deployment that targeted this agent, and the
// requirements that deployment placed on it. Reason is the skip or failure
// text; it is empty when the agent met the requirements and is proceeding.
type AgentAsk struct {
	ModelID string                 `json:"model_id"`
	Status  DeviceDeploymentStatus `json:"status"`
	Reason  string                 `json:"reason,omitempty"`
	Needs   Requirements           `json:"needs"`
}
