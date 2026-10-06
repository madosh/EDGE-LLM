// Requests are relative (/api/...). The server adds X-Api-Key and forwards
// them to the control plane: the Vite proxy in `npm run dev`, and
// src/hooks.server.ts in the production build. The key never reaches the browser.

export interface Device {
	id: string;
	name: string;
	labels: Record<string, string>;
	status: 'online' | 'offline';
	last_seen_at: string;
	current_model_id: string | null;
	agent_version: string;
	arch: string;
	os: string;
	mem_total_mb: number;
	accelerators: string[];
	created_at: string;
}

export interface TelemetryPoint {
	device_id: string;
	model_id: string;
	tps: number;
	ttft_ms: number;
	mem_mb: number;
	/** 'running', or 'error' when the probe against the runtime failed. */
	status: string;
	/** 'probe' = measured on the real runtime; 'stub' = synthetic demo data. */
	source: string;
	ts: string;
}

export interface DeviceDeployment {
	deployment_id: string;
	device_id: string;
	device_name: string;
	status:
		| 'pending'
		| 'downloading'
		| 'verifying'
		| 'running'
		| 'failed'
		| 'skipped'
		| 'rolled_back';
	error_msg: string | null;
	updated_at: string;
}

export interface Deployment {
	id: string;
	model_id: string;
	artifact_url: string;
	artifact_sha256: string;
	tag_selector: Record<string, string>;
	rollout_percent: number;
	requirements: Requirements;
	status: 'pending' | 'in_progress' | 'completed' | 'failed' | 'partial_failure' | 'rolled_back';
	created_at: string;
	completed_at: string | null;
	devices: DeviceDeployment[];
}

/** What a model needs from a device; unset fields mean no requirement. */
export interface Requirements {
	min_mem_mb?: number;
	accelerator?: 'cpu' | 'gpu' | 'npu';
	arch?: string[];
}

/** What happened per device when a deployment was created or promoted. */
export interface Targeting {
	targeted: string[];
	skipped: Record<string, string>;
	held_back: number;
}

export interface RollbackResult {
	deployment_id: string;
	restored: Record<string, string>;
	no_previous: string[];
}

export interface AgentStep {
	tool: string;
	arguments: unknown;
	result: string;
}

export interface AgentTask {
	id: string;
	device_id: string;
	prompt: string;
	max_steps: number;
	status: 'pending' | 'running' | 'done' | 'failed';
	answer: string | null;
	error_msg: string | null;
	steps: AgentStep[];
	model_id: string | null;
	duration_ms: number | null;
	created_at: string;
	completed_at: string | null;
}

export interface ArtifactInfo {
	name: string;
	url: string;
	sha256: string;
}

async function get<T>(path: string): Promise<T> {
	const res = await fetch(path);
	if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
	return res.json();
}

async function post<T>(path: string, body: unknown): Promise<T> {
	const res = await fetch(path, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});
	if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
	return res.json();
}

export interface FleetSummary {
	device_count: number;
	avg_tps: number;
	p50_tps: number;
	p95_tps: number;
	avg_ttft_ms: number;
	p50_ttft_ms: number;
	p95_ttft_ms: number;
	avg_mem_mb: number;
	total_mem_mb: number;
}

export interface FleetTimeSeriesPoint {
	ts: string;
	avg_tps: number;
	avg_ttft_ms: number;
	avg_mem_mb: number;
	device_count: number;
}

export interface DeviceMetric {
	device_id: string;
	model_id: string;
	tps: number;
	ttft_ms: number;
	mem_mb: number;
	status: string;
}

export const api = {
	devices: () => get<Device[]>('/api/devices'),
	device: (id: string) => get<Device>(`/api/devices/${id}`),
	deviceTelemetry: (id: string, limit = 60) =>
		get<TelemetryPoint[]>(`/api/devices/${id}/telemetry?limit=${limit}`),
	deployments: () => get<Deployment[]>('/api/deployments'),
	artifacts: () => get<ArtifactInfo[]>('/api/artifacts'),
	createDeployment: (body: {
		model_id: string;
		artifact_url: string;
		artifact_sha256: string;
		tag_selector: Record<string, string>;
		rollout_percent?: number;
		requirements?: Requirements;
		all_devices?: boolean;
	}) => post<Deployment & { targeting: Targeting }>('/api/deployments', body),
	promoteDeployment: (id: string, rollout_percent: number) =>
		post<Deployment & { targeting: Targeting }>(`/api/deployments/${id}/promote`, { rollout_percent }),
	rollbackDeployment: (id: string) => post<RollbackResult>(`/api/deployments/${id}/rollback`, {}),

	// On-device AI agent
	askDevice: (deviceId: string, prompt: string) =>
		post<AgentTask>(`/api/devices/${deviceId}/tasks`, { prompt }),
	deviceTasks: (deviceId: string) => get<AgentTask[]>(`/api/devices/${deviceId}/tasks`),

	// Fleet-wide telemetry
	fleetSummary: (window = '5m') =>
		get<FleetSummary>(`/api/telemetry/fleet?window=${window}`),
	fleetTimeSeries: (window = '30m', bucket = 30) =>
		get<FleetTimeSeriesPoint[]>(`/api/telemetry/timeseries?window=${window}&bucket=${bucket}`),
	fleetDevices: () => get<DeviceMetric[]>('/api/telemetry/devices'),
	telemetryAlerts: (threshold = 10) =>
		get<DeviceMetric[]>(`/api/telemetry/alerts?threshold=${threshold}`)
};
