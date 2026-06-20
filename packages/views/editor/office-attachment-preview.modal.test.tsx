import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, cleanup } from "@testing-library/react";
import { useEffect } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Attachment } from "@multica/core/types";

// vi.hoisted: all mock fns referenced from vi.mock factories must be hoisted so
// they're defined before the (hoisted) factory runs.
const mocks = vi.hoisted(() => ({
  getOfficeConfig: vi.fn(),
  getBaseUrl: vi.fn(() => ""),
  downloadMock: vi.fn(),
  openExternalMock: vi.fn(),
}));

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    api: {
      getOfficeConfig: mocks.getOfficeConfig,
      getBaseUrl: mocks.getBaseUrl,
    },
  };
});

vi.mock("./utils/docs-api-loader", () => ({
  loadDocsApi: vi.fn(() => new Promise(() => {})), // never resolves; we only assert config fetch
}));

vi.mock("./use-download-attachment", () => ({
  useDownloadAttachment: () => mocks.downloadMock,
}));

vi.mock("../platform", () => ({
  openExternal: mocks.openExternalMock,
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/ws/issues",
    searchParams: new URLSearchParams(),
    getShareableUrl: vi.fn((p: string) => `https://app.example${p}`),
  }),
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspaceSlug: () => "ws",
  };
});

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (sel: (s: Record<string, Record<string, string>>) => string) =>
      sel({
        image: { download: "Download" },
        attachment: {
          preview: "Preview",
          preview_loading: "Loading preview…",
          preview_failed: "Couldn't load preview",
          preview_too_large: "File is too large to preview. Please download.",
          preview_unsupported: "This file type can't be previewed.",
          close: "Close",
          download_failed: "",
          open_in_new_tab: "Open in new tab",
        },
        file_card: {
          enter_full_screen: "Enter full screen",
          exit_full_screen: "Exit full screen",
        },
      }),
  }),
}));

import { useAttachmentPreview } from "./attachment-preview-modal";

const officeAttachment: Attachment = {
  id: "att-office-1",
  workspace_id: "ws",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "member",
  uploader_id: "u",
  filename: "Report.docx",
  url: "https://cdn.x/Report.docx",
  download_url: "https://cdn.x/Report.docx",
  markdown_url: "",
  content_type:
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  size_bytes: 100,
  created_at: "",
};

function Harness({ attachment }: { attachment: Attachment }) {
  const preview = useAttachmentPreview();
  useEffect(() => {
    preview.open({ kind: "full", attachment });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  return <>{preview.modal}</>;
}

function renderWithClient(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("AttachmentPreviewModal — office kind", () => {
  beforeEach(() => mocks.getOfficeConfig.mockReset());
  afterEach(() => cleanup());

  it("mounts the OnlyOffice viewer for an office attachment", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "https://weboffice.x", config: {} });
    renderWithClient(<Harness attachment={officeAttachment} />);
    await waitFor(() => expect(mocks.getOfficeConfig).toHaveBeenCalledWith("att-office-1"));
  });
});
