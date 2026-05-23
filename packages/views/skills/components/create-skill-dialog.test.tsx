// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
import zhHansCommon from "../../locales/zh-Hans/common.json";
import zhHansSkills from "../../locales/zh-Hans/skills.json";
import { CreateSkillDialog } from "./create-skill-dialog";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    createSkill: vi.fn(),
    importSkill: vi.fn(),
    importLocalSkills: vi.fn(),
  },
}));

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

describe("CreateSkillDialog", () => {
  it("offers upload folder or zip as a creation method", () => {
    render(<CreateSkillDialog onClose={vi.fn()} />, { wrapper });
    expect(screen.getByRole("button", { name: /Upload folder or zip/i })).toBeInTheDocument();
  });

  it("uses the requested Chinese title for local upload", () => {
    render(<CreateSkillDialog onClose={vi.fn()} />, { wrapper: zhHansWrapper });
    expect(screen.getByRole("button", { name: /上传 文件夹 或 zip压缩包/ })).toBeInTheDocument();
  });
});
