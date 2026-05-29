import { type ReactNode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
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

// The dialog's live-activity fallback (useLiveTaskActivity) subscribes via
// useWSEvent. Capture the handlers so tests can fire task:activity / task:message.
const { wsHandlers } = vi.hoisted(() => ({
  wsHandlers: new Map<string, (payload: unknown) => void>(),
}));
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
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
  it("redacts secrets before rendering inline edit diffs", () => {
    const rawSecret = "sk-proj-oldsecret1234567890abcdef";
    const items: TimelineItem[] = [
      {
        seq: 1,
        type: "tool_use",
        tool: "edit_file",
        input: {
          file_path: "E:/workspace/tests/.env",
          old_string: `OPENAI_API_KEY=${rawSecret}`,
          new_string: "OPENAI_API_KEY=sk-proj-newsecret1234567890abcdef",
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

    fireEvent.click(screen.getByText(".../tests/.env"));

    expect(screen.queryByText(rawSecret, { exact: false })).not.toBeInTheDocument();
    expect(screen.getAllByText((content) => content.includes("[REDACTED")).length).toBeGreaterThan(0);
  });

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

  it("renders non-diff edit tool results as text", () => {
    const items: TimelineItem[] = [
      {
        seq: 1,
        type: "tool_result",
        tool: "patch_apply",
        output: "patched: src/app.ts",
      },
    ];

    render(
      <AgentTranscriptDialog
        open={true}
        onOpenChange={() => {}}
        task={baseTask()}
        items={items}
        agentName="Codex"
      />,
      { wrapper: I18nWrapper },
    );

    fireEvent.click(screen.getByText("patched: src/app.ts"));

    expect(screen.getAllByText("patched: src/app.ts").length).toBeGreaterThan(1);
    expect(screen.queryByText("No visual diff available for this file change.")).not.toBeInTheDocument();
  });
});

describe("AgentTranscriptDialog live activity", () => {
  function renderLive(activity?: string) {
    return render(
      <AgentTranscriptDialog
        open={true}
        onOpenChange={() => {}}
        task={{ ...baseTask(), status: "running" as const }}
        items={[]}
        agentName="Codex"
        isLive
        activity={activity}
      />,
      { wrapper: I18nWrapper },
    );
  }
  const fireActivity = (value: string, afterSeq = 0) =>
    act(() => {
      wsHandlers
        .get("task:activity")
        ?.({ task_id: "task-1", activity: value, after_seq: afterSeq });
    });
  const fireMessage = (seq: number) =>
    act(() => {
      wsHandlers.get("task:message")?.({ task_id: "task-1", seq, type: "tool_use" });
    });

  it("shows the live stage, not a static 'waiting for events', in the empty live state", () => {
    renderLive();
    expect(screen.getByText("Thinking")).toBeInTheDocument();
  });

  it("reflects the parent's reconnect hint so it matches the live card on (re)open", () => {
    renderLive("reconnecting");
    expect(screen.getByText("Reconnecting")).toBeInTheDocument();
  });

  it("with no prop, a task:activity reconnect hint shows Reconnecting (lazy fallback)", () => {
    renderLive();
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    fireActivity("reconnecting", 0);
    expect(screen.getByText("Reconnecting")).toBeInTheDocument();
  });

  it("a task:message with a higher seq clears the stale fallback hint", () => {
    renderLive();
    fireActivity("reconnecting", 0);
    expect(screen.getByText("Reconnecting")).toBeInTheDocument();
    fireMessage(1); // seq 1 > after_seq 0 → supersedes
    expect(screen.queryByText("Reconnecting")).not.toBeInTheDocument();
    expect(screen.getByText("Thinking")).toBeInTheDocument();
  });

  it("the activity prop takes priority over the fallback subscription", () => {
    renderLive("reconnecting"); // prop set
    fireActivity("reconnecting", 0); // fallback also set
    fireMessage(5); // clears the fallback (5 > 0) — but the prop drives the display
    expect(screen.getByText("Reconnecting")).toBeInTheDocument();
  });
});
