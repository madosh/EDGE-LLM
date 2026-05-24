export interface FleetEvent {
	type: string;
	device_id?: string;
	deployment_id?: string;
	[key: string]: unknown;
}

type EventHandler = (event: FleetEvent) => void;

let source: EventSource | null = null;
let handlers: EventHandler[] = [];
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

function connect() {
	if (source && source.readyState !== EventSource.CLOSED) return;

	source = new EventSource('/api/events/stream');

	source.onmessage = (e) => {
		try {
			const event: FleetEvent = JSON.parse(e.data);
			handlers.forEach((h) => h(event));
		} catch {
			// ignore non-JSON messages
		}
	};

	source.onerror = () => {
		source?.close();
		source = null;
		if (!reconnectTimer) {
			reconnectTimer = setTimeout(() => {
				reconnectTimer = null;
				connect();
			}, 3000);
		}
	};
}

export function subscribeEvents(handler: EventHandler): () => void {
	handlers.push(handler);
	connect();

	return () => {
		handlers = handlers.filter((h) => h !== handler);
		if (handlers.length === 0 && source) {
			source.close();
			source = null;
		}
	};
}
