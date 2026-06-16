import { describe, expect, it } from "vitest";

import type { TaskMessagePayload } from "../types/events";
import {
  isTaskMessageTaskId,
  mergeTaskMessagesBySeq,
  taskMessagesOptions,
} from "./queries";

function tm(seq: number, content: string): TaskMessagePayload {
  return { task_id: "t", issue_id: "i", seq, type: "text", content };
}

describe("taskMessagesOptions", () => {
  it("fetches task messages for persisted UUID task ids", () => {
    const taskId = "4a2e8d1c-7f9b-4e2a-9c1d-123456789abc";

    expect(isTaskMessageTaskId(taskId)).toBe(true);
    expect(taskMessagesOptions(taskId).enabled).toBe(true);
  });

  it("does not fetch task messages for optimistic task ids", () => {
    const taskId = "optimistic-optimistic-1778739487737";

    expect(isTaskMessageTaskId(taskId)).toBe(false);
    expect(taskMessagesOptions(taskId).enabled).toBe(false);
  });
});

describe("mergeTaskMessagesBySeq", () => {
  it("unions both sides by seq, ascending", () => {
    const merged = mergeTaskMessagesBySeq(
      [tm(1, "a"), tm(3, "c")],
      [tm(2, "b"), tm(4, "d")],
    );

    expect(merged.map((m) => m.seq)).toEqual([1, 2, 3, 4]);
  });

  it("keeps messages present only in the current (WS-appended) side", () => {
    // A `task:message` (seq 2) landed via WS while an HTTP catch-up that only
    // saw seq 1 was in flight; merging must not drop it.
    const merged = mergeTaskMessagesBySeq([tm(1, "a"), tm(2, "ws")], [tm(1, "a")]);

    expect(merged.map((m) => m.seq)).toEqual([1, 2]);
    expect(merged.find((m) => m.seq === 2)?.content).toBe("ws");
  });

  it("lets the fetched copy win on a seq collision", () => {
    const merged = mergeTaskMessagesBySeq([tm(1, "stale")], [tm(1, "fresh")]);

    expect(merged).toHaveLength(1);
    expect(merged[0]?.content).toBe("fresh");
  });
});
