import { type ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { TaskMessagePayload } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import { AgentActivityLabel } from "./agent-activity-label";

function wrap(children: ReactNode) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon } }}>
      {children}
    </I18nProvider>,
  );
}

const msg = (m: Partial<TaskMessagePayload>) => m as TaskMessagePayload;

describe("AgentActivityLabel", () => {
  it("shows the tool label while a command is running", () => {
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[msg({ type: "tool_use", tool: "bash" })]}
      />,
    );
    expect(screen.getByText("Running a command")).toBeInTheDocument();
  });

  it("shows Thinking when running with no messages yet", () => {
    wrap(<AgentActivityLabel status="running" taskMessages={[]} />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
  });

  it("shows Typing while the agent streams text", () => {
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[msg({ type: "text", content: "hi" })]}
      />,
    );
    expect(screen.getByText("Typing")).toBeInTheDocument();
  });

  it("labels the real Codex tools (exec_command / patch_apply), not the Working fallback", () => {
    // Regression guard: the Codex backend emits exactly these slugs, and the
    // earlier bash/read tests masked that they were unmapped. They must render
    // a specific stage, otherwise the feature's core fix is lost for Codex.
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[msg({ type: "tool_use", tool: "exec_command" })]}
      />,
    );
    expect(screen.getByText("Running a command")).toBeInTheDocument();
  });

  it("labels the Codex edit tool (patch_apply) as Making edits", () => {
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[msg({ type: "tool_use", tool: "patch_apply" })]}
      />,
    );
    expect(screen.getByText("Making edits")).toBeInTheDocument();
  });

  it("shows Reconnecting when a reconnecting activity hint is set, overriding the tool stage", () => {
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[msg({ type: "tool_use", tool: "exec_command" })]}
        activity="reconnecting"
      />,
    );
    expect(screen.getByText("Reconnecting")).toBeInTheDocument();
    expect(screen.queryByText("Running a command")).not.toBeInTheDocument();
  });

  it("keeps the tool label after a trailing tool_result (the inter-tool gap)", () => {
    // The 6–12s "model is deciding the next tool" gap: a tool_result lands but
    // no new tool_use yet. The label must stay on the last tool, not reset to
    // a generic state, so the panel doesn't look frozen.
    wrap(
      <AgentActivityLabel
        status="running"
        taskMessages={[
          msg({ type: "tool_use", tool: "read" }),
          msg({ type: "tool_result" }),
        ]}
      />,
    );
    expect(screen.getByText("Reading files")).toBeInTheDocument();
  });
});
