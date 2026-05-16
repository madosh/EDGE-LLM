// All requests go through the Vite dev-server proxy which injects X-Api-Key
// and forwards to control-plane:8080.

export interface Device {
	id: string;
	name: string;
	labels: Record<string, string>;
	status: 'online' | 'offline';
	last_seen_at: string;
	current_model_id: string | null;
	agent_version: string;
	created_at: string;
}

export interface TelemetryPoint {
	device_id: string;
	model_id: string;
	tps: number;
	ttft_ms: number;
	mem_mb: number;
	status: string;
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
	status: 'pending' | 'in_progress' | 'completed' | 'failed';
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
	}) => post<Deployment>('/api/deployments', body)
};
