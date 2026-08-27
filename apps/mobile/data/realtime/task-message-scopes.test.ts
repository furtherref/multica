import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { WSClient } from "./ws-client";
import { bindTaskMessageScopes } from "./task-message-scopes";

const TASK_ID = "11111111-1111-4111-8111-111111111111";

describe("bindTaskMessageScopes", () => {
  it("subscribes before fetch and releases after the final observer", async () => {
    const order: string[] = [];
    const releaseScope = vi.fn(() => order.push("unsubscribe"));
    const ws = {
      on: vi.fn(() => () => {}),
      subscribeScope: vi.fn(() => {
        order.push("subscribe");
        return releaseScope;
      }),
    } as unknown as WSClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const unbind = bindTaskMessageScopes(queryClient, ws);
    const options = {
      queryKey: ["task-messages", TASK_ID] as const,
      queryFn: async () => {
        order.push("fetch");
        return [];
      },
    };

    const first = new QueryObserver(queryClient, options);
    const releaseFirst = first.subscribe(() => {});
    const second = new QueryObserver(queryClient, options);
    const releaseSecond = second.subscribe(() => {});

    await vi.waitFor(() => expect(order).toContain("fetch"));
    expect(order[0]).toBe("subscribe");
    expect(ws.subscribeScope).toHaveBeenCalledTimes(1);

    releaseFirst();
    expect(releaseScope).not.toHaveBeenCalled();
    releaseSecond();
    expect(releaseScope).toHaveBeenCalledOnce();
    unbind();
  });

  it("ignores malformed and non-task query keys", () => {
    const ws = {
      on: vi.fn(() => () => {}),
      subscribeScope: vi.fn(() => () => {}),
    } as unknown as WSClient;
    const queryClient = new QueryClient();
    const unbind = bindTaskMessageScopes(queryClient, ws);
    const observers = [
      new QueryObserver(queryClient, {
        queryKey: ["task-messages", "not-a-uuid"],
        queryFn: async () => [],
      }),
      new QueryObserver(queryClient, {
        queryKey: ["issues", TASK_ID],
        queryFn: async () => [],
      }),
      new QueryObserver(queryClient, {
        queryKey: ["task-messages", TASK_ID, "extra"],
        queryFn: async () => [],
      }),
    ];
    const releases = observers.map((observer) => observer.subscribe(() => {}));

    expect(ws.subscribeScope).not.toHaveBeenCalled();

    for (const release of releases) release();
    unbind();
  });

  it("applies task messages only while that transcript is observed", () => {
    let onTaskMessage: ((payload: {
      task_id: string;
      issue_id?: string;
      seq: number;
      type: "text";
      content?: string;
    }) => void) | undefined;
    const ws = {
      subscribeScope: vi.fn(() => () => {}),
      on: vi.fn((event: string, handler: typeof onTaskMessage) => {
        if (event === "task:message") onTaskMessage = handler;
        return () => {};
      }),
    } as unknown as WSClient;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const unbind = bindTaskMessageScopes(queryClient, ws);
    const observer = new QueryObserver(queryClient, {
      queryKey: ["task-messages", TASK_ID],
      queryFn: async () => [],
    });
    const releaseObserver = observer.subscribe(() => {});

    onTaskMessage?.({ task_id: TASK_ID, seq: 1, type: "text", content: "live" });
    expect(queryClient.getQueryData(["task-messages", TASK_ID])).toEqual([
      { task_id: TASK_ID, seq: 1, type: "text", content: "live" },
    ]);

    releaseObserver();
    onTaskMessage?.({ task_id: TASK_ID, seq: 2, type: "text", content: "late" });
    expect(queryClient.getQueryData<unknown[]>(["task-messages", TASK_ID])).toHaveLength(1);
    unbind();
  });
});
