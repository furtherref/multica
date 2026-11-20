import { render, screen } from "@testing-library/react";
import { act } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { UpdateNotification } from "./update-notification";

type UpdateAvailableCallback = (info: { version: string; releaseNotes?: string }) => void;
type DownloadProgressCallback = (progress: { percent: number }) => void;
type UpdateDownloadedCallback = (info: { version: string; releaseNotes?: string }) => void;

const callbacks = {
  updateAvailable: null as UpdateAvailableCallback | null,
  downloadProgress: null as DownloadProgressCallback | null,
  updateDownloaded: null as UpdateDownloadedCallback | null,
};

beforeEach(() => {
  callbacks.updateAvailable = null;
  callbacks.downloadProgress = null;
  callbacks.updateDownloaded = null;

  // Partial mocks, as upstream's test does: a strict DesktopAPI literal breaks
  // on every new preload field even though this component reads only these.
  Object.defineProperty(window, "desktopAPI", {
    configurable: true,
    value: { openExternal: vi.fn(() => Promise.resolve()) },
  });

  Object.defineProperty(window, "updater", {
    configurable: true,
    value: {
    onUpdateAvailable: vi.fn((callback: UpdateAvailableCallback) => {
      callbacks.updateAvailable = callback;
      return vi.fn();
    }),
    onDownloadProgress: vi.fn((callback: DownloadProgressCallback) => {
      callbacks.downloadProgress = callback;
      return vi.fn();
    }),
    onUpdateDownloaded: vi.fn((callback: UpdateDownloadedCallback) => {
      callbacks.updateDownloaded = callback;
      return vi.fn();
    }),
    downloadUpdate: vi.fn(() => Promise.resolve()),
    installUpdate: vi.fn(() => Promise.resolve()),
    getPreferences: vi.fn(() => Promise.resolve({ automaticUpdates: true })),
    setAutomaticUpdates: vi.fn((enabled: boolean) => Promise.resolve({ automaticUpdates: enabled })),
    checkForUpdates: vi.fn(() =>
      Promise.resolve({
        ok: true,
        currentVersion: "0.2.24",
        latestVersion: "0.2.25",
        available: true,
      } as const),
    ),
    },
  });

});

describe("UpdateNotification", () => {
  it("shows immediate loading feedback after clicking Restart now", async () => {
    render(<UpdateNotification />);

    act(() => {
      callbacks.updateDownloaded?.({ version: "0.2.25" });
    });

    const restartButton = await screen.findByRole("button", { name: "Restart now" });
    act(() => {
      restartButton.click();
    });

    expect(window.updater.installUpdate).toHaveBeenCalledOnce();
    expect(
      screen.getByRole("button", { name: /restarting/i }),
    ).toBeDisabled();
  });

  // The prompt names a version but nothing about it; the changelog anchor is
  // what lets someone decide whether to restart now or finish what they are
  // doing first.
  it("opens the downloaded version's changelog", async () => {
    render(<UpdateNotification />);

    act(() => {
      callbacks.updateDownloaded?.({ version: "0.4.27" });
    });

    const changelogButton = await screen.findByRole("button", {
      name: "See changelog",
    });
    act(() => {
      changelogButton.click();
    });

    expect(window.desktopAPI.openExternal).toHaveBeenCalledWith(
      "https://multica.furtherref.com/changelog#release-0-4-27",
    );
  });
});
