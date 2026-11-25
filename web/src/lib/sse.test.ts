import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectSSE, type SSEConnectionStatus } from "@/lib/sse";

class MockEventSource extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: MockEventSource[] = [];

  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSED = 2;

  readyState = MockEventSource.CONNECTING;
  closed = false;
  readonly url: string;

  constructor(url: string) {
    super();
    this.url = url;
    MockEventSource.instances.push(this);
  }

  close() {
    this.readyState = MockEventSource.CLOSED;
    this.closed = true;
  }

  emitOpen() {
    this.readyState = MockEventSource.OPEN;
    this.dispatchEvent(new Event("open"));
  }

  emitError(nextReadyState: number) {
    this.readyState = nextReadyState;
    this.dispatchEvent(new Event("error"));
  }

  emitMessage(type: string, data: unknown) {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

describe("connectSSE", () => {
  const originalEventSource = window.EventSource;

  beforeEach(() => {
    vi.useFakeTimers();
    MockEventSource.instances = [];
    Object.defineProperty(window, "EventSource", {
      value: MockEventSource,
      writable: true,
      configurable: true,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    Object.defineProperty(window, "EventSource", {
      value: originalEventSource,
      writable: true,
      configurable: true,
    });
  });

  it("recovers with exponential backoff after EventSource enters CLOSED (e.g. 502 or 401)", () => {
    const statuses: SSEConnectionStatus[] = [];
    const onReconnect = vi.fn();
    const onClosedError = vi.fn();
    const onEvent = vi.fn();

    const disconnect = connectSSE({
      url: "/api/v1/events",
      onEvent,
      onStatusChange: (s) => statuses.push(s),
      onReconnect,
      onClosedError,
      initialBackoffMs: 1000,
      maxBackoffMs: 4000,
    });

    expect(MockEventSource.instances).toHaveLength(1);
    const first = MockEventSource.instances[0];
    if (!first) {
      throw new Error("expected initial EventSource instance");
    }

    first.emitOpen();
    expect(statuses).toEqual(["connecting", "connected"]);
    expect(onReconnect).not.toHaveBeenCalled();

    // Non-200 response puts EventSource into CLOSED.
    first.emitError(MockEventSource.CLOSED);
    expect(first.closed).toBe(true);
    expect(onClosedError).toHaveBeenCalledTimes(1);
    expect(statuses.at(-1)).toBe("reconnecting");

    // Backoff #1: 1000ms.
    vi.advanceTimersByTime(999);
    expect(MockEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(MockEventSource.instances).toHaveLength(2);

    const second = MockEventSource.instances[1];
    if (!second) {
      throw new Error("expected second EventSource instance");
    }
    second.emitError(MockEventSource.CLOSED);
    expect(onClosedError).toHaveBeenCalledTimes(2);

    // Backoff #2 doubles to 2000ms.
    vi.advanceTimersByTime(1999);
    expect(MockEventSource.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(MockEventSource.instances).toHaveLength(3);

    const third = MockEventSource.instances[2];
    if (!third) {
      throw new Error("expected third EventSource instance");
    }
    third.emitOpen();
    expect(statuses.at(-1)).toBe("connected");
    expect(onReconnect).toHaveBeenCalledTimes(1);

    // Backoff resets after successful open: next CLOSED retries after 1000ms again.
    third.emitError(MockEventSource.CLOSED);
    vi.advanceTimersByTime(1000);
    expect(MockEventSource.instances).toHaveLength(4);

    disconnect();
    // No further reconnects after disconnect.
    vi.advanceTimersByTime(30_000);
    expect(MockEventSource.instances).toHaveLength(4);
  });

  it("handles browser-native transient reconnects without spawning duplicate EventSources", () => {
    const onReconnect = vi.fn();
    const onClosedError = vi.fn();
    const onEvent = vi.fn();

    const disconnect = connectSSE({
      onEvent,
      onStatusChange: vi.fn(),
      onReconnect,
      onClosedError,
    });

    const source = MockEventSource.instances[0];
    if (!source) {
      throw new Error("expected EventSource instance");
    }
    source.emitOpen();
    source.emitError(MockEventSource.CONNECTING);
    expect(onClosedError).not.toHaveBeenCalled();
    vi.advanceTimersByTime(10_000);
    expect(MockEventSource.instances).toHaveLength(1);

    source.emitOpen();
    expect(onReconnect).toHaveBeenCalledTimes(1);
    source.emitMessage("project.created", { project: "shop" });
    expect(onEvent).toHaveBeenCalledWith({
      type: "project.created",
      payload: { project: "shop" },
    });

    disconnect();
  });
});
