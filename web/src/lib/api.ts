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
	status: 'pending' | 'downloading' | 'verifying' | 'running' | 'failed';
	error_msg: string | null;
	updated_at: string;
}

export interface Deployment {
	id: string;
	model_id: string;
	artifact_url: string;
	artifact_sha256: string;
	tag_selector: Record<string, string>;
	status: 'pending' | 'in_progress' | 'completed' | 'failed' | 'partial_failure';
	created_at: string;
	completed_at: string | null;
	devices: DeviceDeployment[];
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
	}) => post<Deployment>('/api/deployments', body),

	// Fleet-wide telemetry
	fleetSummary: (window = '5m') =>
		get<FleetSummary>(`/api/telemetry/fleet?window=${window}`),
	fleetTimeSeries: (window = '30m', bucket = 30) =>
		get<FleetTimeSeriesPoint[]>(`/api/telemetry/timeseries?window=${window}&bucket=${bucket}`),
	fleetDevices: () => get<DeviceMetric[]>('/api/telemetry/devices'),
	telemetryAlerts: (threshold = 10) =>
		get<DeviceMetric[]>(`/api/telemetry/alerts?threshold=${threshold}`)
};
