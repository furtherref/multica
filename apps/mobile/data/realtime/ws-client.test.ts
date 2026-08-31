import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WSClient } from "./ws-client";

class MockWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: MockWebSocket[] = [];

  readyState = MockWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  readonly sent: string[] = [];

  constructor(readonly url: string) {
    MockWebSocket.instances.push(this);
  }

  open() {
    this.readyState = MockWebSocket.OPEN;
    this.onopen?.();
  }

  receive(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }

  send(frame: string) {
    this.sent.push(frame);
  }

  close() {
    this.readyState = MockWebSocket.CLOSED;
    this.onclose?.();
  }
}

function connectAuthenticatedClient() {
  const client = new WSClient({
    url: "wss://example.test/ws",
    token: "token",
    workspaceSlug: "workspace",
  });
  client.connect();
  const socket = MockWebSocket.instances[0];
  socket.open();
  socket.receive({ type: "auth_ack" });
  return { client, socket };
}

describe("WSClient application heartbeat", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", MockWebSocket);
    MockWebSocket.instances = [];
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("reconnects a stale OPEN socket through the jittered backoff path", () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    const { client, socket } = connectAuthenticatedClient();
    expect(new URL(socket.url).searchParams.get("task_scopes")).toBe("1");

    expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "auth", payload: { token: "token" } },
      { type: "ping" },
    ]);

    // No pong arrives even though the JS-visible readyState remains OPEN.
    vi.advanceTimersByTime(10_000);
    vi.advanceTimersByTime(1);

    expect(MockWebSocket.instances).toHaveLength(2);
    client.disconnect();
  });

  it("keeps a healthy socket connected when its pong arrives", () => {
    const { client, socket } = connectAuthenticatedClient();
    socket.receive({ type: "pong" });

    vi.advanceTimersByTime(10_000);

    expect(MockWebSocket.instances).toHaveLength(1);
    client.disconnect();
  });

  it("reference-counts scope subscriptions and unsubscribes after the last release", () => {
    const client = new WSClient({
      url: "wss://example.test/ws",
      token: "token",
      workspaceSlug: "workspace",
    });
    const releaseFirst = client.subscribeScope("task", "task-1");
    const releaseSecond = client.subscribeScope("task", "task-1");

    client.connect();
    const socket = MockWebSocket.instances[0];
    socket.open();
    socket.receive({ type: "auth_ack" });

    expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "auth", payload: { token: "token" } },
      { type: "subscribe", payload: { scope: "task", id: "task-1" } },
      { type: "ping" },
    ]);

    releaseFirst();
    expect(socket.sent).toHaveLength(3);

    releaseSecond();
    expect(JSON.parse(socket.sent.at(-1)!)).toEqual({
      type: "unsubscribe",
      payload: { scope: "task", id: "task-1" },
    });
    client.disconnect();
  });

  it("replays active scopes before reconnect catch-up callbacks", () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    const { client, socket } = connectAuthenticatedClient();
    const release = client.subscribeScope("task", "task-1");
    const callback = vi.fn(() => {
      const reconnectedSocket = MockWebSocket.instances[1];
      expect(JSON.parse(reconnectedSocket.sent.at(-1)!)).toEqual({
        type: "subscribe",
        payload: { scope: "task", id: "task-1" },
      });
    });
    client.onReconnect(callback);

    socket.close();
    vi.advanceTimersByTime(0);
    const reconnectedSocket = MockWebSocket.instances[1];
    reconnectedSocket.open();
    reconnectedSocket.receive({ type: "auth_ack" });

    expect(callback).toHaveBeenCalledOnce();
    expect(reconnectedSocket.sent.map((frame) => JSON.parse(frame))).toEqual([
      { type: "auth", payload: { token: "token" } },
      { type: "subscribe", payload: { scope: "task", id: "task-1" } },
      { type: "ping" },
    ]);
    release();
    client.disconnect();
  });
});
