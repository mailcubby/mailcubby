import type { SSEEvent } from "@/lib/sseReducer";

export type SSEConnectionStatus = "connecting" | "connected" | "reconnecting";

const SUPPORTED_EVENT_TYPES = [
  "message.created",
  "message.updated",
  "message.deleted",
  "project.updated",
  "project.created",
  "project.deleted",
  "membership.changed",
] as const;

export interface ConnectSSEOptions {
  url?: string;
  onEvent: (event: SSEEvent) => void;
  onStatusChange: (status: SSEConnectionStatus) => void;
  onReconnect: () => void;
}

/**
 * Opens a single EventSource stream to `/api/v1/events` and dispatches typed
 * Mailcubby SSE events. Distinguishes the initial connection from reconnects
 * so the caller can invalidate TanStack Query caches on reconnect (ADR 0003).
 */
export function connectSSE({
  url = "/api/v1/events",
  onEvent,
  onStatusChange,
  onReconnect,
}: ConnectSSEOptions): () => void {
  if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
    return () => {};
  }

  let hasConnectedOnce = false;
  let hadDisconnect = false;
  const source = new window.EventSource(url);
  onStatusChange("connecting");

  const handleOpen = () => {
    const isReconnect = hasConnectedOnce || hadDisconnect;
    hasConnectedOnce = true;
    hadDisconnect = false;
    onStatusChange("connected");
    if (isReconnect) {
      onReconnect();
    }
  };

  const handleError = () => {
    hadDisconnect = true;
    onStatusChange("reconnecting");
  };

  source.addEventListener("open", handleOpen);
  source.addEventListener("error", handleError);

  const listeners: Array<{ type: string; fn: (e: Event) => void }> = [];
  for (const eventType of SUPPORTED_EVENT_TYPES) {
    const listener = (raw: Event) => {
      const msgEvent = raw as MessageEvent<string>;
      if (typeof msgEvent.data !== "string" || !msgEvent.data) {
        return;
      }
      try {
        const parsed: unknown = JSON.parse(msgEvent.data);
        if (parsed && typeof parsed === "object") {
          onEvent({
            type: eventType,
            payload: parsed,
          } as SSEEvent);
        }
      } catch {
        // Ignore malformed event frames.
      }
    };
    source.addEventListener(eventType, listener);
    listeners.push({ type: eventType, fn: listener });
  }

  return () => {
    source.removeEventListener("open", handleOpen);
    source.removeEventListener("error", handleError);
    for (const { type, fn } of listeners) {
      source.removeEventListener(type, fn);
    }
    source.close();
  };
}
