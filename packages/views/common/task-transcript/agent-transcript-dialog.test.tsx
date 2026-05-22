import { type ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { AgentTranscriptDialog } from "./agent-transcript-dialog";
import type { TimelineItem } from "./build-timeline";
import type { AgentTask } from "@multica/core/types/agent";

vi.mock("@multica/core/api", () => ({
  api: {
    getAgent: vi.fn().mockResolvedValue(null),
    listRuntimes: vi.fn().mockResolvedValue([]),
  },
}));
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Test WS", slug: "test" }),
}));

vi.mock("../actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    agents: enAgents,
  },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
  return (
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    </QueryClientProvider>
  );
}

function baseTask(): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "completed",
    priority: 1,
    created_at: "2026-05-13T00:00:00Z",
    started_at: "2026-05-13T00:00:10Z",
    completed_at: "2026-05-13T00:00:20Z",
    dispatched_at: "2026-05-13T00:00:00Z",
    result: null,
    error: null,
  };
}

describe("AgentTranscriptDialog tool_use diff rendering", () => {
  it("renders diff for create-file tool_use with content + file_path", () => {
    const items: TimelineItem[] = [
      {
        seq: 1,
        type: "tool_use",
        tool: "write_file",
        input: {
          file_path: "E:/workspace/tests/readme.txt",
          content: "hello\nworld\n",
        },
      },
    ];

    render(
      <AgentTranscriptDialog
        open={true}
        onOpenChange={() => {}}
        task={baseTask()}
        items={items}
        agentName="Claude"
      />,
      { wrapper: I18nWrapper },
    );

    fireEvent.click(screen.getByText(".../tests/readme.txt"));

    expect(screen.getByText("File changes")).toBeInTheDocument();
    expect(screen.getByText("--- E:/workspace/tests/readme.txt")).toBeInTheDocument();
    expect(screen.getByText("@@ -0,0 +1,2 @@")).toBeInTheDocument();
    expect(screen.getByText("+hello")).toBeInTheDocument();
    expect(screen.getByText("+world")).toBeInTheDocument();
    expect(screen.queryByText("+")).not.toBeInTheDocument();
    expect(screen.queryByText("No visual diff available for this file change.")).not.toBeInTheDocument();
  });

  it("renders diff for replace tool_use with old_string + new_string", () => {
    const items: TimelineItem[] = [
      {
        seq: 1,
        type: "tool_use",
        tool: "edit_file",
        input: {
          file_path: "E:/workspace/tests/hello.txt",
          old_string: "before",
          new_string: "after",
          replace_all: false,
        },
      },
    ];

    render(
      <AgentTranscriptDialog
        open={true}
        onOpenChange={() => {}}
        task={baseTask()}
        items={items}
        agentName="Claude"
      />,
      { wrapper: I18nWrapper },
    );

    fireEvent.click(screen.getByText(".../tests/hello.txt"));

    expect(screen.getByText("File changes")).toBeInTheDocument();
    expect(screen.getByText("-before")).toBeInTheDocument();
    expect(screen.getByText("+after")).toBeInTheDocument();
    expect(screen.queryByText("No visual diff available for this file change.")).not.toBeInTheDocument();
  });
});
