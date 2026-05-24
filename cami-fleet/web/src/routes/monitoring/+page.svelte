<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { api } from '$lib/api';
	import type { FleetSummary, FleetTimeSeriesPoint, DeviceMetric } from '$lib/api';

	let summary: FleetSummary | null = null;
	let timeSeries: FleetTimeSeriesPoint[] = [];
	let devices: DeviceMetric[] = [];
	let alerts: DeviceMetric[] = [];
	let loading = true;
	let interval: ReturnType<typeof setInterval>;

	async function refresh() {
		try {
			const [s, ts, d, a] = await Promise.all([
				api.fleetSummary('5m'),
				api.fleetTimeSeries('30m', 30),
				api.fleetDevices(),
				api.telemetryAlerts(15)
			]);
			summary = s;
			timeSeries = ts;
			devices = d;
			alerts = a;
		} catch (e) {
			console.error('monitoring fetch error', e);
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		refresh();
		interval = setInterval(refresh, 5000);
	});

	onDestroy(() => clearInterval(interval));

	function sparklinePath(data: number[], width: number, height: number): string {
		if (data.length < 2) return '';
		const max = Math.max(...data, 1);
		const min = Math.min(...data, 0);
		const range = max - min || 1;
		const step = width / (data.length - 1);
		return data
			.map((v, i) => {
				const x = i * step;
				const y = height - ((v - min) / range) * height;
				return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
			})
			.join(' ');
	}
</script>

<svelte:head>
	<title>Monitoring — Cami Fleet</title>
</svelte:head>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div>
			<h1 class="text-2xl font-bold text-white">Fleet Monitoring</h1>
			<p class="text-sm text-gray-400 mt-1">Aggregated inference telemetry across all devices</p>
		</div>
		<span class="text-xs text-gray-500 font-mono">auto-refresh 5s</span>
	</div>

	<!-- Alerts banner -->
	{#if alerts.length > 0}
		<div class="bg-red-950/50 border border-red-800/50 rounded-lg p-4">
			<div class="flex items-center gap-2 mb-2">
				<span class="w-2 h-2 rounded-full bg-red-500 animate-pulse"></span>
				<span class="text-sm font-semibold text-red-300">{alerts.length} device{alerts.length > 1 ? 's' : ''} below performance threshold</span>
			</div>
			<div class="flex flex-wrap gap-2">
				{#each alerts as alert}
					<span class="text-xs bg-red-900/50 border border-red-800/30 text-red-200 px-2 py-1 rounded">
						{alert.device_id.slice(0, 8)}… — {alert.tps.toFixed(1)} tps
					</span>
				{/each}
			</div>
		</div>
	{/if}

	<!-- Summary cards -->
	{#if summary}
		<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
			<div class="card">
				<div class="text-xs text-gray-400 uppercase tracking-wide">Active Devices</div>
				<div class="text-2xl font-bold text-white mt-1">{summary.device_count}</div>
			</div>
			<div class="card">
				<div class="text-xs text-gray-400 uppercase tracking-wide">Avg Tokens/sec</div>
				<div class="text-2xl font-bold text-violet-400 mt-1">{summary.avg_tps.toFixed(1)}</div>
				<div class="text-xs text-gray-500 mt-1">p50: {summary.p50_tps.toFixed(1)} · p95: {summary.p95_tps.toFixed(1)}</div>
			</div>
			<div class="card">
				<div class="text-xs text-gray-400 uppercase tracking-wide">Avg TTFT</div>
				<div class="text-2xl font-bold text-emerald-400 mt-1">{summary.avg_ttft_ms.toFixed(0)} ms</div>
				<div class="text-xs text-gray-500 mt-1">p50: {summary.p50_ttft_ms.toFixed(0)} · p95: {summary.p95_ttft_ms.toFixed(0)}</div>
			</div>
			<div class="card">
				<div class="text-xs text-gray-400 uppercase tracking-wide">Avg Memory</div>
				<div class="text-2xl font-bold text-amber-400 mt-1">{summary.avg_mem_mb.toFixed(0)} MB</div>
				<div class="text-xs text-gray-500 mt-1">per device average</div>
			</div>
		</div>
	{:else if loading}
		<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
			{#each Array(4) as _}
				<div class="card animate-pulse h-24"></div>
			{/each}
		</div>
	{/if}

	<!-- Time series chart -->
	{#if timeSeries.length > 1}
		<div class="card">
			<div class="flex items-center justify-between mb-4">
				<h2 class="text-sm font-semibold text-white">Fleet Performance (last 30 min)</h2>
				<div class="flex gap-4 text-xs text-gray-400">
					<span class="flex items-center gap-1"><span class="w-3 h-0.5 bg-violet-400 inline-block"></span> TPS</span>
					<span class="flex items-center gap-1"><span class="w-3 h-0.5 bg-emerald-400 inline-block"></span> TTFT</span>
					<span class="flex items-center gap-1"><span class="w-3 h-0.5 bg-amber-400 inline-block"></span> Mem</span>
				</div>
			</div>
			<div class="relative h-40">
				<svg class="w-full h-full" viewBox="0 0 600 120" preserveAspectRatio="none">
					<path
						d={sparklinePath(timeSeries.map(p => p.avg_tps), 600, 120)}
						fill="none" stroke="#a78bfa" stroke-width="2" vector-effect="non-scaling-stroke"
					/>
					<path
						d={sparklinePath(timeSeries.map(p => p.avg_ttft_ms / 10), 600, 120)}
						fill="none" stroke="#34d399" stroke-width="2" vector-effect="non-scaling-stroke"
					/>
					<path
						d={sparklinePath(timeSeries.map(p => p.avg_mem_mb / 100), 600, 120)}
						fill="none" stroke="#fbbf24" stroke-width="2" vector-effect="non-scaling-stroke"
					/>
				</svg>
			</div>
			<div class="flex justify-between text-xs text-gray-500 mt-2">
				<span>30 min ago</span>
				<span>now</span>
			</div>
		</div>
	{/if}

	<!-- Per-device table -->
	<div class="card">
		<h2 class="text-sm font-semibold text-white mb-4">Device Performance (latest)</h2>
		{#if devices.length > 0}
			<div class="overflow-x-auto">
				<table class="w-full text-sm">
					<thead>
						<tr class="text-xs text-gray-400 uppercase tracking-wide border-b border-gray-800">
							<th class="text-left py-2 pr-4">Device</th>
							<th class="text-left py-2 pr-4">Model</th>
							<th class="text-right py-2 pr-4">TPS</th>
							<th class="text-right py-2 pr-4">TTFT (ms)</th>
							<th class="text-right py-2 pr-4">Memory (MB)</th>
							<th class="text-left py-2">Status</th>
						</tr>
					</thead>
					<tbody>
						{#each devices as d}
							<tr class="border-b border-gray-800/50 hover:bg-gray-800/30">
								<td class="py-2 pr-4">
									<a href="/devices/{d.device_id}" class="text-violet-400 hover:text-violet-300 font-mono text-xs">
										{d.device_id.slice(0, 12)}…
									</a>
								</td>
								<td class="py-2 pr-4 text-gray-300">{d.model_id || '—'}</td>
								<td class="py-2 pr-4 text-right font-mono" class:text-red-400={d.tps < 15} class:text-green-400={d.tps >= 15}>
									{d.tps.toFixed(1)}
								</td>
								<td class="py-2 pr-4 text-right font-mono text-gray-300">{d.ttft_ms.toFixed(0)}</td>
								<td class="py-2 pr-4 text-right font-mono text-gray-300">{d.mem_mb.toFixed(0)}</td>
								<td class="py-2">
									<span class="inline-flex items-center gap-1.5">
										<span class="w-1.5 h-1.5 rounded-full" class:bg-green-500={d.status === 'running'} class:bg-yellow-500={d.status !== 'running'}></span>
										<span class="text-xs text-gray-400">{d.status}</span>
									</span>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{:else}
			<p class="text-gray-500 text-sm">No telemetry data yet. Deploy a model to start seeing metrics.</p>
		{/if}
	</div>
</div>
