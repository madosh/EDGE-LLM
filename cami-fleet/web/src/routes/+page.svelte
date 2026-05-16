<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { api, type Device } from '$lib/api';

	let devices: Device[] = [];
	let error = '';
	let loading = true;
	let interval: ReturnType<typeof setInterval>;

	async function load() {
		try {
			devices = await api.devices();
			error = '';
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		load();
		interval = setInterval(load, 5000);
	});

	onDestroy(() => clearInterval(interval));

	$: online = devices.filter((d) => d.status === 'online').length;
	$: offline = devices.filter((d) => d.status === 'offline').length;

	function labelChips(labels: Record<string, string>): string[] {
		return Object.entries(labels).map(([k, v]) => `${k}=${v}`);
	}

	function relTime(ts: string): string {
		const diff = Date.now() - new Date(ts).getTime();
		const s = Math.floor(diff / 1000);
		if (s < 60) return `${s}s ago`;
		if (s < 3600) return `${Math.floor(s / 60)}m ago`;
		return `${Math.floor(s / 3600)}h ago`;
	}
</script>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-end justify-between">
		<div>
			<h1 class="text-2xl font-bold text-white">Fleet Overview</h1>
			<p class="text-sm text-gray-500 mt-1">Registered edge devices</p>
		</div>
		<a href="/deployments" class="btn-primary">Deploy Model</a>
	</div>

	<!-- Stats -->
	<div class="grid grid-cols-3 gap-4">
		{#each [['Total', devices.length, 'text-white'], ['Online', online, 'text-emerald-400'], ['Offline', offline, 'text-gray-500']] as [label, count, cls]}
			<div class="card text-center">
				<div class="text-3xl font-bold {cls}">{count}</div>
				<div class="text-xs text-gray-500 mt-1">{label}</div>
			</div>
		{/each}
	</div>

	{#if loading}
		<div class="text-gray-500 text-sm">Loading...</div>
	{:else if error}
		<div class="card border-red-900 text-red-400 text-sm">Error: {error}</div>
	{:else if devices.length === 0}
		<div class="card text-center py-12 text-gray-500 text-sm">
			No devices registered yet. Start the device agents.
		</div>
	{:else}
		<!-- Device table -->
		<div class="card p-0 overflow-hidden">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-gray-800 text-gray-400 text-xs uppercase tracking-wide">
						<th class="text-left px-5 py-3 font-medium">Device</th>
						<th class="text-left px-5 py-3 font-medium">Status</th>
						<th class="text-left px-5 py-3 font-medium">Labels</th>
						<th class="text-left px-5 py-3 font-medium">Model</th>
						<th class="text-left px-5 py-3 font-medium">Last Seen</th>
					</tr>
				</thead>
				<tbody>
					{#each devices as d (d.id)}
						<tr class="border-b border-gray-800/50 hover:bg-gray-800/30 transition-colors">
							<td class="px-5 py-3">
								<a href="/devices/{d.id}" class="font-mono text-violet-300 hover:text-violet-200 transition-colors">
									{d.name}
								</a>
								<div class="text-xs text-gray-600 mt-0.5">{d.id.slice(0, 8)}…</div>
							</td>
							<td class="px-5 py-3">
								{#if d.status === 'online'}
									<span class="badge-online">
										<span class="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
										online
									</span>
								{:else}
									<span class="badge-offline">
										<span class="w-1.5 h-1.5 rounded-full bg-gray-500"></span>
										offline
									</span>
								{/if}
							</td>
							<td class="px-5 py-3">
								<div class="flex flex-wrap gap-1">
									{#each labelChips(d.labels) as chip}
										<span class="text-xs bg-gray-800 text-gray-300 px-2 py-0.5 rounded font-mono">{chip}</span>
									{/each}
								</div>
							</td>
							<td class="px-5 py-3 font-mono text-xs text-gray-300">
								{d.current_model_id ?? '—'}
							</td>
							<td class="px-5 py-3 text-xs text-gray-500">{relTime(d.last_seen_at)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
