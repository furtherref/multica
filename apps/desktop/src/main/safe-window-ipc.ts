import type { BrowserWindow } from "electron";

export function safeSendToWindow(
  win: BrowserWindow | null,
  channel: string,
  ...args: unknown[]
): boolean {
  if (!win || win.isDestroyed()) return false;
  if (win.webContents.isDestroyed()) return false;
  try {
    win.webContents.send(channel, ...args);
    return true;
  } catch {
    return false;
  }
}

export function isLiveWindow(win: BrowserWindow | null): win is BrowserWindow {
  return !!win && !win.isDestroyed() && !win.webContents.isDestroyed();
}
