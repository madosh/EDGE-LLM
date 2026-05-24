import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

const API_KEY = process.env.API_KEY || 'changeme';
const CONTROL_PLANE = process.env.CONTROL_PLANE_URL || 'http://control-plane:8080';

export default defineConfig({
	plugins: [sveltekit()],
	server: {
		host: '0.0.0.0',
		port: 5173,
		proxy: {
			'/api': {
				target: CONTROL_PLANE,
				changeOrigin: true,
				headers: { 'X-Api-Key': API_KEY }
			},
			'/artifacts': {
				target: CONTROL_PLANE,
				changeOrigin: true
			}
		}
	}
});
