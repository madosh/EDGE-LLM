import adapter from '@sveltejs/adapter-node';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	// Components use <script lang="ts">; Svelte 4 needs a preprocessor to
	// strip TypeScript syntax (e.g. `import { type Device }`) before compiling.
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter()
	}
};

export default config;
