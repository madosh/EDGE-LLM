<script lang="ts">
	import '../app.css';
</script>

<div class="min-h-screen flex flex-col">
	<!-- Nav -->
	<header class="border-b border-gray-800 bg-gray-950/80 backdrop-blur sticky top-0 z-10">
		<div class="max-w-7xl mx-auto px-6 h-14 flex items-center gap-8">
			<a href="/" class="flex items-center gap-2 font-bold text-white text-sm tracking-tight">
				<span class="text-violet-400">◈</span> Cami Fleet
			</a>
			<nav class="flex gap-5 text-sm text-gray-400">
				<a href="/" class="hover:text-white transition-colors">Fleet</a>
				<a href="/deployments" class="hover:text-white transition-colors">Deployments</a>
			</nav>
			<span class="ml-auto text-xs text-gray-600 font-mono">v0.1.0</span>
		</div>
	</header>

	<main class="flex-1 max-w-7xl mx-auto w-full px-6 py-8">
		<slot />
	</main>
</div>
