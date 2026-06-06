"use client";

import { useCallback } from "react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceSlug } from "@multica/core/paths";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useT } from "../i18n";

interface DesktopBridge {
  downloadURL?: (u: string) => Promise<void> | void;
}

function attachmentDownloadEndpoint(
  attachmentId: string,
  workspaceSlug: string,
): string {
  const params = new URLSearchParams({ workspace_slug: workspaceSlug });
  const path = `/api/attachments/${encodeURIComponent(attachmentId)}/download`;
  const endpoint = `${path}?${params.toString()}`;
  return resolvePublicFileUrl(endpoint) ?? endpoint;
}

function triggerBrowserDownload(url: string): void {
  const anchor = document.createElement("a");
  anchor.href = url;
  // Keep the click in the current browsing context. For same-origin API
  // downloads this hint lets Chromium/Safari use Content-Disposition's
  // filename without opening a blank tab. If the endpoint later 302s to
  // CloudFront/S3, the server signs that redirect with an attachment
  // disposition; the browser follows it natively without buffering the file
  // into JS memory.
  anchor.download = "";
  anchor.rel = "noopener";
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

// Detected at call time, not module load — the bridge is injected by the
// Electron preload after `window` exists, and reading it lazily lets the
// same hook work in both renderers without a build-time fork.
function hasDesktopDownloadBridge(): boolean {
  if (typeof window === "undefined") return false;
  const bridge = (window as unknown as { desktopAPI?: DesktopBridge }).desktopAPI;
  return Boolean(bridge?.downloadURL);
}

/**
 * Returns a callback that downloads an attachment by ID. The Web path uses
 * the unified server endpoint directly instead of opening a blank tab or
 * materializing the file as a Blob in renderer memory.
 *
 * Two execution shapes, picked at call time:
 *
 * - **Web**: first refreshes attachment metadata for the existing error
 *   feedback path, then clicks a temporary same-origin
 *   `/api/attachments/{id}/download?workspace_slug=...` anchor. The backend
 *   endpoint owns CloudFront / S3 presign / proxy selection and download
 *   Content-Disposition, so large files stay in the browser's native download
 *   pipeline.
 *
 * - **Desktop**: hands the attachment's public storage `url` to
 *   `desktopAPI.downloadURL()`, which invokes Electron's native
 *   `webContents.downloadURL()` to show a save dialog and write the file
 *   directly. It downloads the storage URL rather than the access-controlled
 *   `download_url` endpoint because a main-process `downloadURL` request can't
 *   carry the renderer's Bearer token (the endpoint would 401 and Electron
 *   would save the error body). This also avoids the system browser entirely
 *   and fixes the Linux/Ubuntu issue where HTML files are rendered inline
 *   instead of being downloaded.
 */
export function useDownloadAttachment(): (attachmentId: string) => Promise<void> {
  const { t } = useT("editor");
  const workspaceSlug = useWorkspaceSlug();
  return useCallback(
    async (attachmentId: string) => {
      const failed = () => toast.error(t(($) => $.attachment.download_failed));

      if (hasDesktopDownloadBridge()) {
        try {
          const fresh = await api.getAttachment(attachmentId);
          // Download from the attachment's public storage `url` — the same
          // directly-reachable address the inline thumbnail and preview load
          // from. `webContents.downloadURL` is a main-process session request
          // that can't carry the renderer's Bearer token, so the
          // access-controlled `download_url` endpoint
          // (/api/attachments/{id}/download) rejects it and Electron saves the
          // error body (named `download.txt`). The storage URL needs no auth;
          // Electron derives the filename from its path
          // (`workspaces/{ws}/{uuid}.{ext}`), so the saved name is
          // `{uuid}.{ext}`. Fall back to `download_url` only when `url` is
          // missing.
          //
          // `resolvePublicFileUrl` resolves any server-relative form against
          // the configured API base — Electron's `downloadURLSafely` requires
          // an http(s)-parsable URL — and passes absolute URLs through.
          const rawUrl = fresh.url || fresh.download_url;
          const downloadUrl = resolvePublicFileUrl(rawUrl) ?? rawUrl;
          if (!downloadUrl) {
            failed();
            return;
          }
          const bridge = (
            window as unknown as { desktopAPI?: DesktopBridge }
          ).desktopAPI;
          await bridge!.downloadURL!(downloadUrl);
        } catch {
          failed();
        }
        return;
      }

      try {
        // Keep the preflight metadata request so permission/API failures still
        // produce the existing toast instead of a silent failed navigation. Do
        // not use `download_url` here: in CloudFront mode it may already be a
        // signed CDN URL, while the unified endpoint is the stable browser
        // entry point that chooses cloudfront / presign / proxy server-side.
        await api.getAttachment(attachmentId);
        if (typeof document === "undefined") {
          failed();
          return;
        }
        if (!workspaceSlug) {
          failed();
          return;
        }
        triggerBrowserDownload(
          attachmentDownloadEndpoint(attachmentId, workspaceSlug),
        );
      } catch {
        failed();
      }
    },
    [t, workspaceSlug],
  );
}
