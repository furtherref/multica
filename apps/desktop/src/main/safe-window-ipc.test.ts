import { describe, expect, it, vi } from "vitest";
import type { BrowserWindow } from "electron";
import { safeSendToWindow } from "./safe-window-ipc";

function makeWindow({
  windowDestroyed = false,
  webContentsDestroyed = false,
  sendImpl,
}: {
  windowDestroyed?: boolean;
  webContentsDestroyed?: boolean;
  sendImpl?: (...args: unknown[]) => void;
} = {}) {
  const send = vi.fn(sendImpl);
  return {
    isDestroyed: () => windowDestroyed,
    webContents: {
      isDestroyed: () => webContentsDestroyed,
      send,
    },
  };
}

function asBrowserWindow(win: ReturnType<typeof makeWindow>): BrowserWindow {
  return win as unknown as BrowserWindow;
}

describe("safeSendToWindow", () => {
  it("sends to a live BrowserWindow", () => {
    const win = makeWindow();

    expect(
      safeSendToWindow(asBrowserWindow(win), "daemon:status", {
        state: "stopped",
      }),
    ).toBe(true);

    expect(win.webContents.send).toHaveBeenCalledWith("daemon:status", {
      state: "stopped",
    });
  });

  it("does not send to a destroyed BrowserWindow", () => {
    const win = makeWindow({ windowDestroyed: true });

    expect(
      safeSendToWindow(asBrowserWindow(win), "daemon:status", {
        state: "stopped",
      }),
    ).toBe(false);

    expect(win.webContents.send).not.toHaveBeenCalled();
  });

  it("does not send to destroyed webContents", () => {
    const win = makeWindow({ webContentsDestroyed: true });

    expect(
      safeSendToWindow(asBrowserWindow(win), "daemon:status", {
        state: "stopped",
      }),
    ).toBe(false);

    expect(win.webContents.send).not.toHaveBeenCalled();
  });

  it("does not throw when send fails because the window was destroyed", () => {
    const win = makeWindow({
      sendImpl: () => {
        throw new Error("Object has been destroyed");
      },
    });

    expect(() =>
      safeSendToWindow(asBrowserWindow(win), "daemon:status", {
        state: "stopped",
      }),
    ).not.toThrow();

    expect(
      safeSendToWindow(asBrowserWindow(win), "daemon:status", {
        state: "stopped",
      }),
    ).toBe(false);
  });
});
