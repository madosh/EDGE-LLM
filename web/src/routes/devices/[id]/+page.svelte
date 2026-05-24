<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { api, type Device, type TelemetryPoint } from '$lib/api';

	let device: Device | null = null;
	let telemetry: TelemetryPoint[] = [];
	let error = '';
	let interval: ReturnType<typeof setInterval>;

	$: id = $page.params.id;

	async function load() {
		try {
			[device, telemetry] = await Promise.all([api.device(id), api.deviceTelemetry(id, 30)]);
			error = '';
		} catch (e: any) {
			error = e.message;
		}
	}

	onMount(() => {
		load();
		interval = setInterval(load, 3000);
	});
	onDestroy(() => clearInterval(interval));

	// Sparkline helpers
	function sparkPath(points: TelemetryPoint[], key: 'tps' | 'ttft_ms' | 'mem_mb'): string {
		if (points.length < 2) return '';
		const vals = [...points].reverse().map((p) => p[key]);
		const min = Math.min(...vals);
		const max = Math.max(...vals) || 1;
		const w = 200,
			h = 40;
		return vals
			.map((v, i) => {
				const x = (i / (vals.length - 1)) * w;
				const y = h - ((v - min) / (max - min || 1)) * h;
				return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
			})
			.join(' ');
	}

	$: latest = telemetry[0];
</script>

<div class="space-y-6">
	<!-- Back -->
	<a href="/" class="text-xs text-gray-500 hover:text-gray-300 transition-colors">← Fleet overview</a>

	{#if error}
		<div class="card border-red-900 text-red-400 text-sm">Error: {error}</div>
	{:else if !device}
		<div class="text-gray-500 text-sm">Loading...</div>
	{:else}
		<!-- Device header -->
		<div class="flex items-start justify-between">
			<div>
				<h1 class="text-2xl font-bold font-mono text-white">{device.name}</h1>
				<div class="text-xs text-gray-500 mt-1 font-mono">{device.id}</div>
			</div>
			{#if device.status === 'online'}
				<span class="badge-online text-sm px-3 py-1">
					<span class="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
					online
				</span>
			{:else}
				<span class="badge-offline text-sm px-3 py-1">
					<span class="w-2 h-2 rounded-full bg-gray-500"></span>
					offline
				</span>
			{/if}
		</div>

		<!-- Info grid -->
		<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
			{#each [['Agent', device.agent_version], ['Model', device.current_model_id ?? '—'], ['Last seen', new Date(device.last_seen_at).toLocaleTimeString()]] as [label, value]}
				<div class="card">
					<div class="text-xs text-gray-500">{label}</div>
					<div class="text-sm font-mono text-gray-200 mt-1 truncate">{value}</div>
				</div>
			{/each}
			<div class="card">
				<div class="text-xs text-gray-500">Labels</div>
				<div class="flex flex-wrap gap-1 mt-1">
					{#each Object.entries(device.labels) as [k, v]}
						<span class="text-xs bg-gray-800 text-gray-300 px-1.5 py-0.5 rounded font-mono">{k}={v}</span>
					{/each}
				</div>
			</div>
		</div>

		<!-- Live telemetry -->
		<div>
			<h2 class="text-sm font-semibold text-gray-400 uppercase tracking-wide mb-3">Live Telemetry</h2>
			{#if telemetry.length === 0}
				<div class="card text-sm text-gray-500 text-center py-8">
					No telemetry yet — waiting for a model to run.
				</div>
			{:else}
				<div class="grid grid-cols-3 gap-4">
					{#each [{ label: 'Tokens / sec', key: 'tps', unit: 'tok/s', color: '#a78bfa' }, { label: 'TTFT', key: 'ttft_ms', unit: 'ms', color: '#34d399' }, { label: 'Memory', key: 'mem_mb', unit: 'MB', color: '#60a5fa' }] as metric}
						<div class="card">
							<div class="text-xs text-gray-500">{metric.label}</div>
							<div class="text-2xl font-bold mt-1 font-mono" style="color:{metric.color}">
								{latest ? latest[metric.key].toFixed(1) : '—'}
								<span class="text-sm text-gray-500 font-normal">{metric.unit}</span>
							</div>
							<!-- Sparkline -->
							<svg viewBox="0 0 200 40" class="w-full mt-2 h-8" preserveAspectRatio="none">
								<path
									d={sparkPath(telemetry, metric.key)}
									fill="none"
									stroke={metric.color}
									stroke-width="1.5"
									stroke-linejoin="round"
									stroke-linecap="round"
									opacity="0.7"
								/>
							</svg>
						</div>
					{/each}
				</div>

				<!-- Telemetry table -->
				<div class="card mt-4 p-0 overflow-hidden">
					<table class="w-full text-xs font-mono">
						<thead>
							<tr class="border-b border-gray-800 text-gray-500">
								<th class="text-left px-4 py-2 font-medium">Time</th>
								<th class="text-right px-4 py-2 font-medium">TPS</th>
								<th class="text-right px-4 py-2 font-medium">TTFT ms</th>
								<th class="text-right px-4 py-2 font-medium">Mem MB</th>
								<th class="text-left px-4 py-2 font-medium">Status</th>
							</tr>
						</thead>
						<tbody>
							{#each telemetry.slice(0, 10) as pt}
								<tr class="border-b border-gray-800/40 hover:bg-gray-800/20">
									<td class="px-4 py-1.5 text-gray-400">{new Date(pt.ts).toLocaleTimeString()}</td>
									<td class="px-4 py-1.5 text-right text-violet-300">{pt.tps.toFixed(1)}</td>
									<td class="px-4 py-1.5 text-right text-emerald-300">{pt.ttft_ms.toFixed(1)}</td>
									<td class="px-4 py-1.5 text-right text-blue-300">{pt.mem_mb.toFixed(0)}</td>
									<td class="px-4 py-1.5 text-gray-400">{pt.status}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>
	{/if}
</div>
