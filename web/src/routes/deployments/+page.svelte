<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { api, type Deployment, type ArtifactInfo } from '$lib/api';

	let deployments: Deployment[] = [];
	let artifacts: ArtifactInfo[] = [];
	let error = '';
	let formError = '';
	let submitting = false;
	let interval: ReturnType<typeof setInterval>;

	// Form state
	let selectedArtifact: ArtifactInfo | null = null;
	let tagSelectorRaw = 'location=barcelona';

	async function load() {
		try {
			[deployments, artifacts] = await Promise.all([api.deployments(), api.artifacts()]);
			if (!selectedArtifact && artifacts.length > 0) selectedArtifact = artifacts[0];
			error = '';
		} catch (e: any) {
			error = e.message;
		}
	}

	onMount(() => {
		load();
		interval = setInterval(load, 5000);
	});
	onDestroy(() => clearInterval(interval));

	function parseTagSelector(raw: string): Record<string, string> {
		const out: Record<string, string> = {};
		for (const part of raw.split(',')) {
			const [k, v] = part.trim().split('=');
			if (k && v !== undefined) out[k.trim()] = v.trim();
		}
		return out;
	}

	async function submit() {
		if (!selectedArtifact) {
			formError = 'Select an artifact';
			return;
		}
		submitting = true;
		formError = '';
		try {
			await api.createDeployment({
				model_id: selectedArtifact.name,
				artifact_url: selectedArtifact.url,
				artifact_sha256: selectedArtifact.sha256,
				tag_selector: parseTagSelector(tagSelectorRaw)
			});
			await load();
		} catch (e: any) {
			formError = e.message;
		} finally {
			submitting = false;
		}
	}

	const statusColors: Record<string, string> = {
		pending: 'text-yellow-400',
		in_progress: 'text-blue-400',
		completed: 'text-emerald-400',
		failed: 'text-red-400'
	};

	const deviceStatusColors: Record<string, string> = {
		pending: 'text-gray-500',
		downloading: 'text-blue-400',
		verifying: 'text-yellow-400',
		running: 'text-emerald-400',
		failed: 'text-red-400'
	};

	function relTime(ts: string): string {
		const diff = Date.now() - new Date(ts).getTime();
		const s = Math.floor(diff / 1000);
		if (s < 60) return `${s}s ago`;
		if (s < 3600) return `${Math.floor(s / 60)}m ago`;
		return `${Math.floor(s / 3600)}h ago`;
	}
</script>

<div class="space-y-8">
	<div>
		<h1 class="text-2xl font-bold text-white">Deployments</h1>
		<p class="text-sm text-gray-500 mt-1">Push model artifacts to tagged devices</p>
	</div>

	<!-- Deploy form -->
	<div class="card space-y-5">
		<h2 class="text-sm font-semibold text-gray-300 uppercase tracking-wide">New Deployment</h2>

		{#if formError}
			<div class="text-red-400 text-sm bg-red-900/20 rounded px-3 py-2">{formError}</div>
		{/if}

		<div class="grid grid-cols-1 md:grid-cols-2 gap-5">
			<!-- Artifact selector -->
			<div>
				<label class="block text-xs text-gray-400 mb-1.5" for="artifact">Model Artifact</label>
				{#if artifacts.length === 0}
					<div class="text-xs text-gray-500">No artifacts available — run <code class="font-mono">docker compose up artifact-init</code></div>
				{:else}
					<select
						id="artifact"
						bind:value={selectedArtifact}
						class="w-full bg-gray-800 border border-gray-700 text-gray-100 rounded-lg px-3 py-2 text-sm focus:outline-none focus:border-violet-500"
					>
						{#each artifacts as a}
							<option value={a}>{a.name}</option>
						{/each}
					</select>
					{#if selectedArtifact}
						<div class="mt-1.5 text-xs text-gray-600 font-mono truncate">{selectedArtifact.sha256}</div>
					{/if}
				{/if}
			</div>

			<!-- Tag selector -->
			<div>
				<label class="block text-xs text-gray-400 mb-1.5" for="tags">
					Tag Selector
					<span class="text-gray-600">(comma-separated key=value)</span>
				</label>
				<input
					id="tags"
					type="text"
					bind:value={tagSelectorRaw}
					placeholder="location=barcelona,type=jetson"
					class="w-full bg-gray-800 border border-gray-700 text-gray-100 rounded-lg px-3 py-2 text-sm font-mono focus:outline-none focus:border-violet-500"
				/>
				<div class="mt-1.5 text-xs text-gray-600">
					Matches devices with <em>all</em> listed labels
				</div>
			</div>
		</div>

		<div class="flex items-center gap-3">
			<button class="btn-primary" on:click={submit} disabled={submitting}>
				{submitting ? 'Deploying…' : 'Deploy →'}
			</button>
			{#if selectedArtifact}
				<span class="text-xs text-gray-500">
					→ <strong class="text-gray-300">{selectedArtifact.name}</strong>
					to devices tagged <strong class="text-gray-300 font-mono">{tagSelectorRaw}</strong>
				</span>
			{/if}
		</div>
	</div>

	<!-- Deployment history -->
	{#if error}
		<div class="card border-red-900 text-red-400 text-sm">Error: {error}</div>
	{:else if deployments.length === 0}
		<div class="card text-center py-10 text-gray-500 text-sm">No deployments yet.</div>
	{:else}
		<div class="space-y-4">
			{#each deployments as dep (dep.id)}
				<div class="card space-y-3">
					<div class="flex items-start justify-between gap-4">
						<div>
							<div class="font-mono text-white text-sm">{dep.model_id}</div>
							<div class="text-xs text-gray-600 mt-0.5">{dep.id.slice(0, 8)}… · {relTime(dep.created_at)}</div>
						</div>
						<div class="flex items-center gap-3">
							<!-- Tag selector badges -->
							<div class="flex gap-1">
								{#each Object.entries(dep.tag_selector) as [k, v]}
									<span class="text-xs bg-gray-800 text-gray-400 px-2 py-0.5 rounded font-mono">{k}={v}</span>
								{/each}
							</div>
							<span class="text-xs font-medium {statusColors[dep.status] || 'text-gray-400'} uppercase tracking-wide">
								{dep.status.replace('_', ' ')}
							</span>
						</div>
					</div>

					<!-- Per-device status -->
					{#if dep.devices && dep.devices.length > 0}
						<div class="border-t border-gray-800 pt-3 grid grid-cols-2 md:grid-cols-3 gap-2">
							{#each dep.devices as dd}
								<div class="flex items-center justify-between bg-gray-800/50 rounded-lg px-3 py-2">
									<span class="text-xs font-mono text-gray-300">{dd.device_name || dd.device_id.slice(0,8)}</span>
									<span class="text-xs font-medium {deviceStatusColors[dd.status] || 'text-gray-400'}">{dd.status}</span>
								</div>
							{/each}
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</div>
