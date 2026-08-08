import { describe, it, expect } from "vitest";
import { effectiveTaskStatus } from "./task-status-pill";

describe("effectiveTaskStatus", () => {
  it("keeps deferred authoritative over task messages from the earlier attempt", () => {
    expect(
      effectiveTaskStatus("deferred", [
        {
          task_id: "task-1",
          issue_id: "",
          seq: 1,
          type: "thinking",
          created_at: "2026-07-01T00:00:00Z",
        },
      ]),
    ).toBe("deferred");
  });

  it("promotes to running once task messages have streamed in", () => {
    expect(
      effectiveTaskStatus("queued", [
        {
          task_id: "task-1",
          issue_id: "",
          seq: 1,
          type: "thinking",
          created_at: "2026-07-01T00:00:00Z",
        },
      ]),
    ).toBe("running");
  });

  it("falls back to the server status when no messages have streamed", () => {
    expect(effectiveTaskStatus("queued", [])).toBe("queued");
  });
});
