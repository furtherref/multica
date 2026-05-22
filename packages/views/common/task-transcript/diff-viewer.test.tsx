import { type ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { DiffViewer } from "./diff-viewer";

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    agents: enAgents,
  },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("DiffViewer", () => {
  it("renders unified and split diff modes", () => {
    render(
      <DiffViewer
        output={[
          "--- a/file.txt",
          "+++ b/file.txt",
          "@@ -1 +1 @@",
          "-old line",
          "+new line",
        ].join("\n")}
      />,
      { wrapper: I18nWrapper },
    );

    expect(
      screen.getByRole("button", { name: "Switch to split diff view" }),
    ).toBeInTheDocument();
    expect(screen.getByText("-old line")).toBeInTheDocument();
    expect(screen.getByText("+new line")).toBeInTheDocument();
    expect(screen.queryByText("old line")).not.toBeInTheDocument();
    expect(screen.queryByText("new line")).not.toBeInTheDocument();

    render(
      <DiffViewer
        output={[
          "--- a/file.txt",
          "+++ b/file.txt",
          "@@ -1 +1 @@",
          "-old line",
          "+new line",
        ].join("\n")}
        defaultMode="split"
      />,
      { wrapper: I18nWrapper },
    );
    expect(
      screen.getByRole("button", { name: "Switch to unified diff view" }),
    ).toBeInTheDocument();
    expect(screen.getByText("old line")).toBeInTheDocument();
    expect(screen.getByText("new line")).toBeInTheDocument();
  });

  it("switches mode when clicking the toggle", () => {
    render(
      <DiffViewer
        output={[
          "--- a/file.txt",
          "+++ b/file.txt",
          "@@ -1 +1 @@",
          "-old line",
          "+new line",
        ].join("\n")}
      />,
      { wrapper: I18nWrapper },
    );

    expect(screen.getByText("-old line")).toBeInTheDocument();
    expect(screen.queryByText("old line")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Switch to split diff view" }));

    expect(screen.getByText("old line")).toBeInTheDocument();
    expect(screen.getByText("new line")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Switch to unified diff view" }),
    ).toBeInTheDocument();
  });

  it("shows placeholder when no visual diff can be parsed", () => {
    render(<DiffViewer output="patched successfully" />, { wrapper: I18nWrapper });

    expect(
      screen.getByText("No visual diff available for this file change."),
    ).toBeInTheDocument();
  });

  it("renders simplified diff card for new-file headers without +/- hunks", () => {
    render(
      <DiffViewer
        output={[
          "--- src/new-file.ts",
          "+++ src/new-file.ts",
          "(new file, 42 bytes)",
        ].join("\n")}
      />,
      { wrapper: I18nWrapper },
    );

    expect(screen.getByText("File changes")).toBeInTheDocument();
    expect(screen.queryByText("No visual diff available for this file change.")).not.toBeInTheDocument();
    expect(screen.getByText("--- src/new-file.ts")).toBeInTheDocument();
    expect(screen.getByText("(new file, 42 bytes)")).toBeInTheDocument();
  });

  it("does not emit a phantom deletion line for new-file writes", () => {
    render(
      <DiffViewer newText={"hello\nworld\n"} filePath="src/greeting.ts" />,
      { wrapper: I18nWrapper },
    );

    expect(screen.getByText("+hello")).toBeInTheDocument();
    expect(screen.getByText("+world")).toBeInTheDocument();
    expect(screen.queryByText("-")).not.toBeInTheDocument();
    expect(screen.getByText("@@ -0,0 +1,2 @@")).toBeInTheDocument();
  });

  it("does not emit a phantom addition line for full-file deletions", () => {
    render(
      <DiffViewer oldText={"hello\nworld\n"} filePath="src/greeting.ts" />,
      { wrapper: I18nWrapper },
    );

    expect(screen.getByText("-hello")).toBeInTheDocument();
    expect(screen.getByText("-world")).toBeInTheDocument();
    expect(screen.queryByText("+")).not.toBeInTheDocument();
    expect(screen.getByText("@@ -1,2 +0,0 @@")).toBeInTheDocument();
  });
});
