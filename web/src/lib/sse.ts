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
  onClosedError?: () => void;
  initialBackoffMs?: number;
  maxBackoffMs?: number;
}

const DEFAULT_INITIAL_BACKOFF_MS = 1000;
const DEFAULT_MAX_BACKOFF_MS = 30_000;

/**
 * Opens an EventSource stream to `/api/v1/events` and dispatches typed
 * Mailcubby SSE events. Distinguishes the initial connection from reconnects
 * so the caller can invalidate TanStack Query caches on reconnect (ADR 0003).
 *
 * When a non-200 response (such as 502 during a reverse-proxy restart or 401
 * on session expiry) puts EventSource into `CLOSED`, the browser stops retrying
 * permanently; this client tears down the dead instance and reconnects with
 * bounded exponential backoff while notifying `onClosedError`.
 */
export function connectSSE({
  url = "/api/v1/events",
  onEvent,
  onStatusChange,
  onReconnect,
  onClosedError,
  initialBackoffMs = DEFAULT_INITIAL_BACKOFF_MS,
  maxBackoffMs = DEFAULT_MAX_BACKOFF_MS,
}: ConnectSSEOptions): () => void {
  if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
    return () => {};
  }

  let disposed = false;
  let hasConnectedOnce = false;
  let hadDisconnect = false;
  let backoffMs = initialBackoffMs;
  let reconnectTimer: number | null = null;
  let currentCleanup: (() => void) | null = null;

  const scheduleReconnect = () => {
    if (disposed || reconnectTimer !== null) {
      return;
    }
    const delay = backoffMs;
    backoffMs = Math.min(maxBackoffMs, backoffMs * 2);
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      if (!disposed) {
        attach();
      }
    }, delay);
  };

  const attach = () => {
    if (disposed) {
      return;
    }
    currentCleanup?.();
    currentCleanup = null;

    const source = new window.EventSource(url);
    onStatusChange(hasConnectedOnce || hadDisconnect ? "reconnecting" : "connecting");

    const listeners: Array<{ type: string; fn: (e: Event) => void }> = [];

    const detach = () => {
      source.removeEventListener("open", handleOpen);
      source.removeEventListener("error", handleError);
      for (const { type, fn } of listeners) {
        source.removeEventListener(type, fn);
      }
      source.close();
    };

    const handleOpen = () => {
      if (disposed) {
        return;
      }
      const isReconnect = hasConnectedOnce || hadDisconnect;
      hasConnectedOnce = true;
      hadDisconnect = false;
      backoffMs = initialBackoffMs;
      onStatusChange("connected");
      if (isReconnect) {
        onReconnect();
      }
    };

    const handleError = () => {
      if (disposed) {
        return;
      }
      hadDisconnect = true;
      onStatusChange("reconnecting");
      const closedState = window.EventSource.CLOSED ?? 2;
      if (source.readyState === closedState) {
        detach();
        if (currentCleanup === detach) {
          currentCleanup = null;
        }
        onClosedError?.();
        scheduleReconnect();
      }
    };

    source.addEventListener("open", handleOpen);
    source.addEventListener("error", handleError);

    for (const eventType of SUPPORTED_EVENT_TYPES) {
      const listener = (raw: Event) => {
        if (disposed) {
          return;
        }
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

    currentCleanup = detach;
  };

  attach();

  return () => {
    disposed = true;
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    currentCleanup?.();
    currentCleanup = null;
  };
}
