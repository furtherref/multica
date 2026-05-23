// @vitest-environment jsdom

import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import JSZip from "jszip";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
import zhHansCommon from "../../locales/zh-Hans/common.json";
import zhHansSkills from "../../locales/zh-Hans/skills.json";

const mockImportLocalSkills = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: { importLocalSkills: (...args: unknown[]) => mockImportLocalSkills(...args) },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { LocalSkillUploadPanel } from "./local-skill-upload-panel";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={{ en: { common: enCommon, skills: enSkills } }}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        {children}
      </QueryClientProvider>
    </I18nProvider>
  );
}

function zhHansWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="zh-Hans" resources={{ "zh-Hans": { common: zhHansCommon, skills: zhHansSkills } }}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        {children}
      </QueryClientProvider>
    </I18nProvider>
  );
}

describe("LocalSkillUploadPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockImportLocalSkills.mockResolvedValue({
      created: [{ skill: skill("skill-1", "Review Helper"), source_label: "review" }],
      skipped: [],
      failed: [],
    });
  });

  it("shows an invalid state when no SKILL.md is found", async () => {
    render(<LocalSkillUploadPanel />, { wrapper });
    const input = screen.getByTestId("local-skill-folder-input");
    fireEvent.change(input, { target: { files: [file("notes.txt", "hello")] } });
    expect(await screen.findByText(/No SKILL\.md found/i)).toBeInTheDocument();
  });

  it("previews and imports a single skill", async () => {
    const onImported = vi.fn();
    render(<LocalSkillUploadPanel onImported={onImported} />, { wrapper });

    fireEvent.change(screen.getByTestId("local-skill-folder-input"), {
      target: { files: [file("review/SKILL.md", "---\nname: Review Helper\n---\nBody")] },
    });

    expect(await screen.findByDisplayValue("Review Helper")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Import$/i }));

    await waitFor(() => {
      expect(mockImportLocalSkills).toHaveBeenCalledWith({
        skills: [expect.objectContaining({ name: "Review Helper", content: expect.stringContaining("Body") })],
      });
      expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ name: "Review Helper" }));
    });
  });

  it("previews a dropped zip skill", async () => {
    const zip = new JSZip();
    zip.file("review/SKILL.md", "---\nname: Review Helper\n---\nBody");
    const blob = await zip.generateAsync({ type: "blob" });
    const archive = new File([blob], "review.zip", { type: "application/zip" });

    render(<LocalSkillUploadPanel />, { wrapper });

    fireEvent.drop(screen.getByText(/Drop a skill folder or \.zip here/i).parentElement!, {
      dataTransfer: {
        items: [dataTransferFileItem(archive)],
        files: [archive],
      },
    });

    expect(await screen.findByDisplayValue("Review Helper")).toBeInTheDocument();
    expect(screen.queryByText(/No SKILL\.md found/i)).not.toBeInTheDocument();
  });

  it("lets users pick any local skill batch under the server limit", async () => {
    render(<LocalSkillUploadPanel />, { wrapper });

    fireEvent.change(screen.getByTestId("local-skill-folder-input"), {
      target: {
        files: Array.from({ length: 17 }, (_, index) =>
          file(`team/skill-${String(index).padStart(2, "0")}/SKILL.md`, `# Skill ${index}`),
        ),
      },
    });

    expect(await screen.findByText(/Select up to 16 skills/i)).toBeInTheDocument();
    expect(screen.getByText(/Ready to import 16 skills/i)).toBeInTheDocument();

    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(17);
    expect(checkboxes.slice(0, 16).every((checkbox) => checkbox.getAttribute("aria-checked") === "true")).toBe(true);
    expect(checkboxes[16]).toHaveAttribute("aria-checked", "false");
    expect(checkboxes[16]).toHaveAttribute("aria-disabled", "true");

    fireEvent.click(checkboxes[0]!);
    expect(checkboxes[16]).not.toHaveAttribute("aria-disabled");
    fireEvent.click(checkboxes[16]!);
    expect(checkboxes[16]).toHaveAttribute("aria-checked", "true");

    fireEvent.click(screen.getByRole("button", { name: /^Import$/i }));

    await waitFor(() => {
      expect(mockImportLocalSkills).toHaveBeenCalledWith({
        skills: expect.arrayContaining([
          expect.objectContaining({ name: "skill-01" }),
          expect.objectContaining({ name: "skill-16" }),
        ]),
      });
    });
    expect(mockImportLocalSkills.mock.calls[0]?.[0].skills).toHaveLength(16);
    expect(mockImportLocalSkills.mock.calls[0]?.[0].skills).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ name: "skill-00" })]),
    );
  });

  it("keeps the dialog open on partial success summary", async () => {
    mockImportLocalSkills.mockResolvedValue({
      created: [{ skill: skill("skill-1", "Review Helper"), source_label: "review" }],
      skipped: [{ name: "Existing", reason: "already_exists" }],
      failed: [{ name: "Broken", reason: "missing_skill_md" }],
    });
    const onImported = vi.fn();
    render(<LocalSkillUploadPanel onImported={onImported} />, { wrapper });

    fireEvent.change(screen.getByTestId("local-skill-folder-input"), {
      target: { files: [file("review/SKILL.md", "# Review")] },
    });
    expect(await screen.findByText(/Ready to import "review"/i)).toBeInTheDocument();
    const importButton = await screen.findByRole("button", { name: /^Import$/i });
    await waitFor(() => expect(importButton).not.toBeDisabled());
    fireEvent.click(importButton);

    await waitFor(() => expect(mockImportLocalSkills).toHaveBeenCalled());
    expect(await screen.findByText(/Existing.*already exists/i)).toBeInTheDocument();
    expect(screen.getByText(/Broken.*missing SKILL\.md/i)).toBeInTheDocument();
    expect(onImported).not.toHaveBeenCalled();
  });

  it("localizes upload result reasons", async () => {
    mockImportLocalSkills.mockResolvedValue({
      created: [{ skill: skill("skill-1", "Review Helper"), source_label: "review" }],
      skipped: [{ name: "Existing", reason: "already_exists" }],
      failed: [{ name: "Broken", reason: "missing_skill_md" }],
    });
    render(<LocalSkillUploadPanel />, { wrapper: zhHansWrapper });

    fireEvent.change(screen.getByTestId("local-skill-folder-input"), {
      target: { files: [file("review/SKILL.md", "# Review")] },
    });
    const importButton = await screen.findByRole("button", { name: /^导入$/i });
    await waitFor(() => expect(importButton).not.toBeDisabled());
    fireEvent.click(importButton);

    await waitFor(() => expect(mockImportLocalSkills).toHaveBeenCalled());
    expect(await screen.findByText(/Existing.*已存在/i)).toBeInTheDocument();
    expect(screen.getByText(/Broken.*缺少 SKILL\.md/i)).toBeInTheDocument();
  });
});

function file(path: string, content: string): File & { webkitRelativePath: string } {
  const value = new File([content], path.split("/").at(-1) ?? "file.txt", { type: "text/plain" });
  Object.defineProperty(value, "webkitRelativePath", { value: path });
  return value as File & { webkitRelativePath: string };
}

function dataTransferFileItem(value: File): DataTransferItem {
  return {
    kind: "file",
    type: value.type,
    getAsFile: () => value,
    getAsString: vi.fn(),
    webkitGetAsEntry: () => null,
  } as unknown as DataTransferItem;
}

function skill(id: string, name: string) {
  return {
    id,
    workspace_id: "ws-1",
    name,
    description: "",
    content: "# Skill",
    config: {},
    files: [],
    created_by: "user-1",
    created_at: "2026-05-12T00:00:00Z",
    updated_at: "2026-05-12T00:00:00Z",
  };
}
