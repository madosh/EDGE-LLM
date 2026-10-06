import type { Handle } from '@sveltejs/kit';
import { env } from '$env/dynamic/private';

// In production (`node build`) there is no Vite dev proxy, so this hook does
// its job: requests for /api and /artifacts are forwarded to the control plane
// with the API key added on the server. The browser never sees the key.
const PROXIED = ['/api/', '/artifacts/'];

// Hop-by-hop and host headers must not be forwarded as-is.
const DROP_REQUEST_HEADERS = ['host', 'connection', 'content-length', 'x-api-key', 'authorization'];

export const handle: Handle = async ({ event, resolve }) => {
	const { pathname, search } = event.url;
	if (!PROXIED.some((prefix) => pathname.startsWith(prefix))) {
		return resolve(event);
	}

	const target = new URL(pathname + search, env.CONTROL_PLANE_URL || 'http://control-plane:8080');
	const headers = new Headers(event.request.headers);
	for (const h of DROP_REQUEST_HEADERS) headers.delete(h);
	headers.set('X-Api-Key', env.API_KEY || 'changeme');

	const method = event.request.method;
	const body = method === 'GET' || method === 'HEAD' ? undefined : await event.request.arrayBuffer();

	const upstream = await fetch(target, { method, headers, body, signal: event.request.signal });

	// Copy into a new Response: fetch() responses have immutable headers, which
	// SvelteKit may need to add to. The body stays a stream, so the server-sent
	// events endpoint (/api/events/stream) keeps streaming through the proxy.
	// Node's fetch has already decoded any compression, so drop those headers.
	const responseHeaders = new Headers(upstream.headers);
	responseHeaders.delete('content-encoding');
	responseHeaders.delete('content-length');
	return new Response(upstream.body, {
		status: upstream.status,
		statusText: upstream.statusText,
		headers: responseHeaders
	});
};
