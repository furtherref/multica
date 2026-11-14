// @vitest-environment jsdom

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { AgentTask } from "@multica/core/types/agent";
import type { TaskMessagePayload } from "@multica/core/types/events";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "@multica/core/api";
import { TranscriptButton } from "./transcript-button";
import type { TimelineItem } from "./build-timeline";

const { MockApiError } = vi.hoisted(() => {
  class MockApiError extends Error {
    readonly status: number;
    readonly statusText: string;
    readonly body?: unknown;

    constructor(message: string, status: number, statusText: string, body?: unknown) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }

  return { MockApiError };
});

vi.mock("@multica/core/api", () => ({
  ApiError: MockApiError,
  api: {
    listTaskMessages: vi.fn(),
  },
}));

// Render items so tests can assert what the dialog actually shows.
vi.mock("./agent-transcript-dialog", () => ({
  AgentTranscriptDialog: ({
    open,
    onOpenChange,
    items,
    loadIncomplete,
    loadPending,
    onRetryLoad,
    retrying,
  }: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    items: TimelineItem[];
    loadIncomplete?: boolean;
    loadPending?: boolean;
    onRetryLoad?: () => void;
    retrying?: boolean;
  }) =>
    open ? (
      <div role="dialog">
        <button type="button" onClick={() => onOpenChange(false)}>
          Close
        </button>
        {loadIncomplete && <div data-testid="load-incomplete">incomplete</div>}
        {loadPending && <div data-testid="load-pending">loading</div>}
        {loadIncomplete && onRetryLoad && (
          <button type="button" disabled={retrying} onClick={() => onRetryLoad()}>
            Retry load
          </button>
        )}
        <ul>
          {items.map((item) => (
            <li key={item.seq}>{item.content}</li>
          ))}
        </ul>
      </div>
    ) : null,
}));

// `taskMessagesOptions` is gated on a real UUID via `isTaskMessageTaskId`, so
// a task must carry a UUID for the shared cache path to engage.
const TASK_UUID = "11111111-1111-4111-8111-111111111111";

const completedTask: AgentTask = {
  id: "task-1",
  agent_id: "agent-1",
  runtime_id: "",
  issue_id: "issue-1",
  status: "completed",
  priority: 0,
  dispatched_at: "2026-05-15T10:00:05.000Z",
  started_at: "2026-05-15T10:00:06.000Z",
  completed_at: "2026-05-15T10:00:10.000Z",
  result: null,
  error: null,
  created_at: "2026-05-15T10:00:00.000Z",
};

const runningTask: AgentTask = {
  ...completedTask,
  id: TASK_UUID,
  status: "running",
  completed_at: null,
};

const terminalTask: AgentTask = {
  ...completedTask,
  id: TASK_UUID,
  status: "completed",
};

const items: TimelineItem[] = [
  {
    seq: 1,
    type: "text",
    content: "hello world",
  },
];

function msg(
  seq: number,
  content: string,
  type: TaskMessagePayload["type"] = "text",
): TaskMessagePayload {
  return { task_id: TASK_UUID, issue_id: "issue-1", seq, type, content };
}

function renderWithClient(ui: ReactNode, qc: QueryClient) {
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.listTaskMessages).mockResolvedValue([]);
});

describe("TranscriptButton", () => {
  it("closes the transcript dialog when desktop navigation starts", async () => {
    renderWithClient(
      <TranscriptButton task={completedTask} agentName="Codex" items={items} />,
      makeClient(),
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    act(() => {
      window.dispatchEvent(
        new CustomEvent("multica:navigate", {
          detail: { path: "/acme/inbox?issue=MUL-123" },
        }),
      );
    });

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  it("renders messages from the shared task-messages cache", async () => {
    const qc = makeClient();
    // Simulate the WS `task:message` handler having seeded the cache already.
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockResolvedValue([msg(1, "first line")]);

    renderWithClient(
      <TranscriptButton task={runningTask} agentName="Codex" isLive />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByText("first line")).toBeInTheDocument();
  });

  it("reflects new task messages pushed into the cache while open (no frozen snapshot)", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockResolvedValue([msg(1, "first line")]);

    renderWithClient(
      <TranscriptButton task={runningTask} agentName="Codex" isLive />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));
    expect(await screen.findByText("first line")).toBeInTheDocument();

    // A later WS `task:message` lands in the shared cache while the dialog is
    // open. Use a distinct message type so `buildTimeline` keeps it as its own
    // node (adjacent same-type text fragments coalesce — covered separately in
    // build-timeline.test.ts); here we only assert the live update flows in.
    act(() => {
      qc.setQueryData<TaskMessagePayload[]>(
        ["task-messages", TASK_UUID],
        (old = []) => [...old, msg(2, "second line", "tool_result")],
      );
    });

    expect(await screen.findByText("second line")).toBeInTheDocument();
    expect(screen.getByText("first line")).toBeInTheDocument();
  });

  it("does a server catch-up on open, recovering messages missing from the warm cache", async () => {
    const qc = makeClient();
    // The cache is "fresh" (staleTime Infinity) but only holds the first
    // message — e.g. a `task:message` was dropped across a WS reconnect, and
    // task-messages is never invalidated on reconnect/completion.
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockResolvedValue([
      msg(1, "first line"),
      msg(2, "recovered line", "tool_result"),
    ]);

    renderWithClient(
      <TranscriptButton task={runningTask} agentName="Codex" isLive />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByText("recovered line")).toBeInTheDocument();
    expect(api.listTaskMessages).toHaveBeenCalledWith(TASK_UUID);
  });

  it("keeps a WS append that lands while the on-open catch-up is in flight", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);

    // The catch-up read saw only seq 1 server-side and resolves later — after a
    // WS append has already put seq 2 in the cache. A whole-cache replace would
    // clobber seq 2; a seq merge must preserve it. Assert on the cache directly:
    // it settles deterministically on resolve, free of render-flush timing.
    let resolveFetch: (v: TaskMessagePayload[]) => void = () => {};
    vi.mocked(api.listTaskMessages).mockImplementation(
      () =>
        new Promise<TaskMessagePayload[]>((res) => {
          resolveFetch = res;
        }),
    );

    renderWithClient(
      <TranscriptButton task={runningTask} agentName="Codex" isLive />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));
    expect(await screen.findByText("first line")).toBeInTheDocument();

    // WS append arrives while the catch-up is still pending.
    act(() => {
      qc.setQueryData<TaskMessagePayload[]>(
        ["task-messages", TASK_UUID],
        (old = []) => [...old, msg(2, "second line", "tool_result")],
      );
    });
    expect(await screen.findByText("second line")).toBeInTheDocument();

    // Catch-up now resolves with the stale server snapshot (only seq 1).
    await act(async () => {
      resolveFetch([msg(1, "first line")]);
      await Promise.resolve();
    });

    // The WS-appended seq 2 must survive the catch-up reconciliation.
    const cached = qc.getQueryData<TaskMessagePayload[]>(["task-messages", TASK_UUID]);
    expect(cached?.map((m) => m.seq)).toEqual([1, 2]);
    expect(screen.getByText("second line")).toBeInTheDocument();
  });

  it("marks a terminal warm-cache transcript incomplete while catch-up is still pending", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockImplementation(
      () => new Promise<TaskMessagePayload[]>(() => {}),
    );

    renderWithClient(
      <TranscriptButton task={terminalTask} agentName="Codex" />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("first line")).toBeInTheDocument();
    expect(screen.getByTestId("load-incomplete")).toBeInTheDocument();
    expect(screen.getByTestId("load-pending")).toBeInTheDocument();
  });

  it("keeps warm cached terminal content visible but incomplete when catch-up fails", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockRejectedValue(new Error("network"));

    renderWithClient(
      <TranscriptButton task={terminalTask} agentName="Codex" />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("first line")).toBeInTheDocument();
    expect(screen.getByTestId("load-incomplete")).toBeInTheDocument();
    expect(screen.queryByTestId("load-pending")).not.toBeInTheDocument();
  });

  it("keeps warm cached transcript content visible but incomplete when authoritative catch-up is forbidden", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    vi.mocked(api.listTaskMessages).mockRejectedValue(
      new ApiError("forbidden", 403, "Forbidden"),
    );

    renderWithClient(
      <TranscriptButton task={terminalTask} agentName="Codex" />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByTestId("load-pending")).not.toBeInTheDocument();
    });
    expect(screen.getByText("first line")).toBeInTheDocument();
    expect(screen.getByTestId("load-incomplete")).toBeInTheDocument();
    expect(qc.getQueryData<TaskMessagePayload[]>(["task-messages", TASK_UUID])).toEqual([
      msg(1, "first line"),
    ]);
  });

  it("keeps the incomplete warning visible while a retry catch-up is pending", async () => {
    const qc = makeClient();
    qc.setQueryData(["task-messages", TASK_UUID], [msg(1, "first line")]);
    let resolveRetry: (value: TaskMessagePayload[]) => void = () => {};
    vi.mocked(api.listTaskMessages)
      .mockRejectedValueOnce(new Error("network"))
      .mockImplementationOnce(
        () =>
          new Promise<TaskMessagePayload[]>((resolve) => {
            resolveRetry = resolve;
          }),
      );

    renderWithClient(
      <TranscriptButton task={terminalTask} agentName="Codex" />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    const retry = await screen.findByRole("button", { name: "Retry load" });
    expect(screen.getByTestId("load-incomplete")).toBeInTheDocument();
    expect(screen.queryByTestId("load-pending")).not.toBeInTheDocument();

    fireEvent.click(retry);

    expect(screen.getByTestId("load-incomplete")).toBeInTheDocument();
    expect(screen.getByTestId("load-pending")).toBeInTheDocument();
    expect(retry).toBeDisabled();

    await act(async () => {
      resolveRetry([msg(1, "first line")]);
      await Promise.resolve();
    });

    await waitFor(() => {
      expect(screen.queryByTestId("load-incomplete")).not.toBeInTheDocument();
    });
  });

  it("still opens the dialog when the messages fetch fails (no permanent loading)", async () => {
    const qc = makeClient();
    vi.mocked(api.listTaskMessages).mockRejectedValue(new Error("network"));

    renderWithClient(
      <TranscriptButton task={terminalTask} agentName="Codex" />,
      qc,
    );

    fireEvent.click(screen.getByRole("button", { name: "View transcript" }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
});
