// @vitest-environment jsdom

import { type ButtonHTMLAttributes, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api } from "@multica/core/api";
import type { AgentRuntime, AgentTask } from "@multica/core/types/agent";
import { useTranscriptViewStore } from "@multica/core/agents/stores";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { renderWithI18n } from "../../test/i18n";
import { AgentTranscriptDialog } from "./agent-transcript-dialog";
import type { TimelineItem } from "./build-timeline";

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

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ open, children }: { open: boolean; children: ReactNode }) =>
    open ? <>{children}</> : null,
  DialogContent: ({ children }: { children: ReactNode }) => (
    <div role="dialog">{children}</div>
  ),
  DialogTitle: ({ children }: { children: ReactNode }) => <h2>{children}</h2>,
}));

vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({
    children,
    ...props
  }: ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" {...props}>
      {children}
    </button>
  ),
  DropdownMenuContent: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuSeparator: () => <hr />,
  DropdownMenuCheckboxItem: ({
    checked,
    onCheckedChange,
    children,
  }: {
    checked?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    children: ReactNode;
  }) => (
    <button
      type="button"
      role="menuitemcheckbox"
      aria-checked={checked === true}
      onClick={() => onCheckedChange?.(checked !== true)}
    >
      {children}
    </button>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    className: _className,
  }: ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("@multica/ui/components/ui/collapsible", async () => {
  const React = await import("react");
  const Context = React.createContext<{
    open: boolean;
    onOpenChange?: (open: boolean) => void;
  }>({ open: false });

  return {
    Collapsible: ({
      open,
      onOpenChange,
      children,
    }: {
      open: boolean;
      onOpenChange?: (open: boolean) => void;
      children: ReactNode;
    }) => (
      <Context.Provider value={{ open, onOpenChange }}>{children}</Context.Provider>
    ),
    CollapsibleTrigger: ({
      disabled,
      children,
      className: _className,
    }: ButtonHTMLAttributes<HTMLButtonElement>) => {
      const ctx = React.useContext(Context);
      return (
        <button
          type="button"
          disabled={disabled}
          onClick={() => {
            if (!disabled) ctx.onOpenChange?.(!ctx.open);
          }}
        >
          {children}
        </button>
      );
    },
    CollapsibleContent: ({ children }: { children: ReactNode }) => {
      const ctx = React.useContext(Context);
      return ctx.open ? <div>{children}</div> : null;
    },
  };
});

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

describe("AgentTranscriptDialog catch-up banner", () => {
  const items: TimelineItem[] = [{ seq: 1, type: "text", content: "hello" }];

  function renderBanner(props: {
    loadIncomplete?: boolean;
    loadPending?: boolean;
    onRetryLoad?: () => void;
    retrying?: boolean;
  }) {
    return render(
      <AgentTranscriptDialog
        open={true}
        onOpenChange={() => {}}
        task={baseTask()}
        items={items}
        agentName="Codex"
        {...props}
      />,
      { wrapper: I18nWrapper },
    );
  }

  it("stays silent while the catch-up is still pending (no banner, no retry)", () => {
    renderBanner({
      loadIncomplete: true,
      loadPending: true,
      onRetryLoad: () => {},
      retrying: true,
    });

    expect(
      screen.queryByText(enAgents.transcript.load_incomplete),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: enAgents.transcript.retry }),
    ).not.toBeInTheDocument();
  });

  it("shows the incomplete warning with a retry only after the catch-up fails", () => {
    renderBanner({ loadIncomplete: true, loadPending: false, onRetryLoad: () => {} });

    expect(
      screen.getByText(enAgents.transcript.load_incomplete),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: enAgents.transcript.retry }),
    ).toBeInTheDocument();
  });

  it("keeps copy-all disabled while the transcript is unverified, even while pending", () => {
    renderBanner({ loadIncomplete: true, loadPending: true });

    expect(
      screen.getByRole("button", { name: enAgents.transcript.copy_all }),
    ).toBeDisabled();
  });

  it("explains the disabled copy-all is still loading while the catch-up is pending", () => {
    renderBanner({ loadIncomplete: true, loadPending: true });

    expect(
      screen.getByRole("button", { name: enAgents.transcript.copy_all }),
    ).toHaveAttribute("title", enAgents.transcript.load_pending);
  });

  it("explains the disabled copy-all could not be loaded once the catch-up failed", () => {
    renderBanner({ loadIncomplete: true, loadPending: false });

    expect(
      screen.getByRole("button", { name: enAgents.transcript.copy_all }),
    ).toHaveAttribute("title", enAgents.transcript.load_incomplete);
  });
});

const filterBaseTask: AgentTask = {
  id: "task-1",
  agent_id: "",
  runtime_id: "",
  issue_id: "issue-1",
  status: "completed",
  priority: 0,
  dispatched_at: null,
  started_at: "2026-06-08T08:00:00Z",
  completed_at: "2026-06-08T08:01:00Z",
  result: null,
  error: null,
  created_at: "2026-06-08T08:00:00Z",
};

const liveTask: AgentTask = {
  ...filterBaseTask,
  runtime_id: "runtime-1",
  status: "running",
  completed_at: null,
};

function runtimeFor(provider: string): AgentRuntime {
  return {
    id: "runtime-1",
    workspace_id: "workspace-1",
    daemon_id: "daemon-1",
    name: `${provider} runtime`,
    runtime_mode: "local",
    provider,
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "owner-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-06-08T08:00:00Z",
    updated_at: "2026-06-08T08:00:00Z",
  };
}

const filterItems: TimelineItem[] = [
  {
    seq: 1,
    type: "text",
    content: "Agent summary\nAgent hidden detail",
  },
  {
    seq: 2,
    type: "thinking",
    content: "Thinking summary\nThinking hidden detail",
  },
  {
    seq: 3,
    type: "tool_use",
    tool: "terminal",
    input: { command: "pnpm test" },
  },
];

function renderDialog(
  dialogItems: TimelineItem[] = filterItems,
  options: { task?: AgentTask; isLive?: boolean } = {},
) {
  return renderWithI18n(
    <AgentTranscriptDialog
      open
      onOpenChange={vi.fn()}
      task={options.task ?? filterBaseTask}
      items={dialogItems}
      agentName="Codex"
      isLive={options.isLive}
    />,
  );
}

beforeEach(() => {
  cleanup();
  vi.mocked(api.listRuntimes).mockResolvedValue([]);
  useTranscriptViewStore.setState({
    sortDirection: "chronological",
    preserveFilters: false,
    selectedFilterKeys: [],
    defaultExpanded: false,
  });
});

afterEach(() => {
  cleanup();
});

describe("AgentTranscriptDialog filter persistence", () => {
  it("explains unavailable live events for an empty Antigravity transcript", async () => {
    vi.mocked(api.listRuntimes).mockResolvedValue([runtimeFor("antigravity")]);

    renderDialog([], { task: liveTask, isLive: true });

    expect(
      await screen.findByText(
        "Antigravity does not currently provide live execution events. The transcript will be available after the task completes.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("Waiting for events...")).not.toBeInTheDocument();
  });

  it("keeps waiting for live events from other runtimes", async () => {
    vi.mocked(api.listRuntimes).mockResolvedValue([runtimeFor("hermes")]);

    renderDialog([], { task: liveTask, isLive: true });

    await screen.findByText("hermes runtime");
    // Fork behavior: the live empty state renders the AgentActivityLabel
    // stage (running + no messages → "Thinking") instead of upstream's
    // generic "Waiting for events..." spinner.
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(
      screen.queryByText(/Antigravity does not currently provide/),
    ).not.toBeInTheDocument();
  });

  it("preserves selected filters across dialog remounts when enabled", () => {
    const first = renderDialog();

    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Thinking" }));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: "Preserve filters" }));

    expect(screen.queryByText("Agent summary")).not.toBeInTheDocument();
    expect(screen.getByText(/Thinking summary/)).toBeInTheDocument();
    expect(useTranscriptViewStore.getState().selectedFilterKeys).toEqual(["thinking"]);

    first.unmount();
    renderDialog();

    expect(screen.queryByText("Agent summary")).not.toBeInTheDocument();
    expect(screen.getByText(/Thinking summary/)).toBeInTheDocument();
  });

  it("ignores stale persisted filter keys that are not available in the current transcript", () => {
    useTranscriptViewStore.setState({
      preserveFilters: true,
      selectedFilterKeys: ["thinking"],
    });

    renderDialog([
      {
        seq: 1,
        type: "text",
        content: "Only agent summary\nOnly agent hidden detail",
      },
    ]);

    expect(screen.getByText("Only agent summary")).toBeInTheDocument();
    expect(screen.queryByText("No execution data recorded.")).not.toBeInTheDocument();
  });

  it("expands and collapses every currently visible detailed row", () => {
    renderDialog();

    expect(screen.queryByText(/Agent hidden detail/)).not.toBeInTheDocument();
    expect(screen.queryByText(/"command": "pnpm test"/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Expand visible" }));

    expect(screen.getByText(/Agent hidden detail/)).toBeInTheDocument();
    expect(screen.getByText(/"command": "pnpm test"/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Collapse visible" }));

    expect(screen.queryByText(/Agent hidden detail/)).not.toBeInTheDocument();
    expect(screen.queryByText(/"command": "pnpm test"/)).not.toBeInTheDocument();
  });

  it("uses the default-expanded preference for newly opened transcripts", () => {
    useTranscriptViewStore.setState({ defaultExpanded: true });

    renderDialog();

    expect(screen.getByText(/Agent hidden detail/)).toBeInTheDocument();
    expect(screen.getByText(/"command": "pnpm test"/)).toBeInTheDocument();
  });
});
