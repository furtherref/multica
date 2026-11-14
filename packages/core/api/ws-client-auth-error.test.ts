// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WSClient } from "./ws-client";

// Same fake-WebSocket idiom as ws-client.test.ts: capture the constructed
// instance so tests can drive onopen/onmessage/onclose directly.
class FakeWebSocket {
  static lastInstance: FakeWebSocket | null = null;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 0;
  constructor(_url: string) {
    FakeWebSocket.lastInstance = this;
  }
  close() {}
  send() {}
}

describe("WSClient auth_error handling", () => {
  beforeEach(() => {
    FakeWebSocket.lastInstance = null;
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("stops reconnecting and fires onAuthRejected on ACCOUNT_SUSPENDED auth_error", () => {
    const onAuthRejected = vi.fn();
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const ws = new WSClient("ws://example.test/ws", { onAuthRejected });
    ws.connect();

    FakeWebSocket.lastInstance!.onmessage?.({
      data: JSON.stringify({
        type: "auth_error",
        payload: { error: "account suspended", code: "ACCOUNT_SUSPENDED" },
      }),
    });

    expect(onAuthRejected).toHaveBeenCalledTimes(1);

    // The socket's onclose still fires after the server-initiated close, but
    // it must not schedule a reconnect once the client has recognized the
    // rejection.
    const timerCountBefore = setTimeoutSpy.mock.calls.length;
    FakeWebSocket.lastInstance!.onclose?.();
    expect(setTimeoutSpy.mock.calls.length).toBe(timerCountBefore);
  });

  it("stops reconnecting but does not fire onAuthRejected for a non-suspension auth_error", () => {
    const onAuthRejected = vi.fn();
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const ws = new WSClient("ws://example.test/ws", { onAuthRejected });
    ws.connect();

    FakeWebSocket.lastInstance!.onmessage?.({
      data: JSON.stringify({
        type: "auth_error",
        payload: { error: "invalid token", code: "INVALID_TOKEN" },
      }),
    });

    expect(onAuthRejected).not.toHaveBeenCalled();

    const timerCountBefore = setTimeoutSpy.mock.calls.length;
    FakeWebSocket.lastInstance!.onclose?.();
    expect(setTimeoutSpy.mock.calls.length).toBe(timerCountBefore);
  });

  it("still schedules a reconnect on a normal close when no auth_error was seen", () => {
    const onAuthRejected = vi.fn();
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const ws = new WSClient("ws://example.test/ws", { onAuthRejected });
    ws.connect();

    const timerCountBefore = setTimeoutSpy.mock.calls.length;
    FakeWebSocket.lastInstance!.onclose?.();
    expect(setTimeoutSpy.mock.calls.length).toBe(timerCountBefore + 1);
    expect(onAuthRejected).not.toHaveBeenCalled();
  });

  it("resets the auth-rejected flag on an explicit connect(), allowing reconnect after fresh login", () => {
    const onAuthRejected = vi.fn();
    const setTimeoutSpy = vi.spyOn(globalThis, "setTimeout");
    const ws = new WSClient("ws://example.test/ws", { onAuthRejected });
    ws.connect();

    FakeWebSocket.lastInstance!.onmessage?.({
      data: JSON.stringify({
        type: "auth_error",
        payload: { error: "account suspended", code: "ACCOUNT_SUSPENDED" },
      }),
    });

    // A fresh login re-invokes connect() explicitly (new WS instance).
    ws.connect();

    const timerCountBefore = setTimeoutSpy.mock.calls.length;
    FakeWebSocket.lastInstance!.onclose?.();
    expect(setTimeoutSpy.mock.calls.length).toBe(timerCountBefore + 1);
  });
});
