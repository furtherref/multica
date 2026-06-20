import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// vi.mock factories are hoisted ABOVE these imports — referencing a plain
// top-level `const x = vi.fn()` from inside a factory throws at load time. Use
// vi.hoisted() so the mock fns exist when the factory runs (CLAUDE.md mocking
// convention).
const mocks = vi.hoisted(() => {
  const destroyEditor = vi.fn();
  // A regular function (not an arrow) so the component's `new docs.DocEditor()`
  // is constructable under Vitest v4; it returns the editor stub.
  const DocEditor = vi.fn(function () {
    return { destroyEditor };
  });
  return {
    getOfficeConfig: vi.fn(),
    destroyEditor,
    DocEditor,
    loadDocsApi: vi.fn(),
  };
});
vi.mock("@multica/core/api", () => ({
  api: { getOfficeConfig: mocks.getOfficeConfig },
}));
vi.mock("./utils/docs-api-loader", () => ({
  loadDocsApi: mocks.loadDocsApi,
}));

import { OfficeAttachmentPreview } from "./office-attachment-preview";

function renderWithClient(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("OfficeAttachmentPreview", () => {
  beforeEach(() => {
    mocks.getOfficeConfig.mockReset();
    mocks.DocEditor.mockClear();
    mocks.destroyEditor.mockReset();
    mocks.loadDocsApi.mockReset();
  });
  afterEach(() => cleanup());

  it("loads config, instantiates DocEditor, and destroys on unmount", async () => {
    mocks.getOfficeConfig.mockResolvedValue({
      document_server_url: "https://weboffice.x",
      config: { documentType: "word", token: "jwt" },
    });
    mocks.loadDocsApi.mockResolvedValue({ DocEditor: mocks.DocEditor });

    const { unmount } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-1" onDownload={() => {}} />,
    );

    await waitFor(() => expect(mocks.getOfficeConfig).toHaveBeenCalledWith("att-1"));
    await waitFor(() => expect(mocks.loadDocsApi).toHaveBeenCalledWith("https://weboffice.x"));
    await waitFor(() => expect(mocks.DocEditor).toHaveBeenCalledTimes(1));
    // Second constructor arg is the signed config.
    expect((mocks.DocEditor.mock.calls[0] as unknown[])[1]).toMatchObject({ documentType: "word", token: "jwt" });

    unmount();
    expect(mocks.destroyEditor).toHaveBeenCalledTimes(1);
  });

  it("renders the download fallback when config has no server url", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "", config: {} });
    const onDownload = vi.fn();
    const { findByRole } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-2" onDownload={onDownload} />,
    );
    (await findByRole("button")).click();
    expect(onDownload).toHaveBeenCalled();
    expect(mocks.loadDocsApi).not.toHaveBeenCalled();
  });

  it("renders the download fallback when api.js fails to load", async () => {
    mocks.getOfficeConfig.mockResolvedValue({ document_server_url: "https://weboffice.x", config: {} });
    mocks.loadDocsApi.mockRejectedValue(new Error("failed to load"));
    const onDownload = vi.fn();
    const { findByRole } = renderWithClient(
      <OfficeAttachmentPreview attachmentId="att-3" onDownload={onDownload} />,
    );
    (await findByRole("button")).click();
    expect(onDownload).toHaveBeenCalled();
  });
});
