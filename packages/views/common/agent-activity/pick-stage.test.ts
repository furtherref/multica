import { describe, it, expect } from "vitest";
import { pickStageKeys } from "./pick-stage";

describe("pickStageKeys", () => {
  it("returns queued when status is queued and agent is online", () => {
    expect(pickStageKeys("queued", [], "online")).toEqual({ stageKey: "queued" });
  });

  it("returns offline when status is queued and agent is offline", () => {
    expect(pickStageKeys("queued", [], "offline")).toEqual({
      stageKey: "offline",
      static: true,
    });
  });

  it("returns waiting_local_directory on the daemon-emitted hold status", () => {
    // Daemon publishes this when it dequeues a task but another task owns the
    // local_directory's lock. The pill becomes static (no shimmer) because
    // nothing is actively happening from the user's point of view.
    expect(pickStageKeys("waiting_local_directory", [], "online")).toEqual({
      stageKey: "waiting_local_directory",
      static: true,
    });
  });

  it("waiting_local_directory wins over availability hints", () => {
    // Even if availability says reconnecting/offline, the directory-release
    // status is the more specific signal — surface it.
    expect(
      pickStageKeys("waiting_local_directory", [], "unstable"),
    ).toEqual({ stageKey: "waiting_local_directory", static: true });
    expect(
      pickStageKeys("waiting_local_directory", [], "offline"),
    ).toEqual({ stageKey: "waiting_local_directory", static: true });
  });

  it("returns thinking for running with no messages", () => {
    expect(pickStageKeys("running", [], "online")).toEqual({ stageKey: "thinking" });
  });

  it("returns typing when the latest message is agent text", () => {
    expect(
      pickStageKeys(
        "running",
        [{ type: "text" } as never],
        "online",
      ),
    ).toEqual({ stageKey: "typing" });
  });

  it("surfaces the tool label for the latest tool_use, ignoring trailing tool_result", () => {
    // A tool_result arriving after the tool_use must NOT reset the stage to
    // generic thinking — the user should still see "Running a command" while
    // the model digests the result and decides the next step (the 6–12s gap).
    expect(
      pickStageKeys(
        "running",
        [{ type: "tool_use", tool: "bash" } as never, { type: "tool_result" } as never],
        "online",
      ),
    ).toEqual({ stageKey: "thinking", toolKey: "running_command" });
  });

  it("maps the real Codex tool names (exec_command, patch_apply), not the fallback", () => {
    // The Codex backend emits these exact slugs (server/pkg/agent/codex.go) —
    // the runtime this whole feature targets. They must resolve to specific
    // labels, never the generic "Working" fallback.
    expect(
      pickStageKeys("running", [{ type: "tool_use", tool: "exec_command" } as never], "online"),
    ).toEqual({ stageKey: "thinking", toolKey: "running_command" });
    expect(
      pickStageKeys("running", [{ type: "tool_use", tool: "patch_apply" } as never], "online"),
    ).toEqual({ stageKey: "thinking", toolKey: "making_edits" });
  });

  it("surfaces a reconnecting hint over the message-derived running stage", () => {
    // The backend just told us it's reconnecting upstream — that's more
    // current than the last tool_use, so the panel should say "Reconnecting"
    // rather than a stale "Running a command".
    expect(
      pickStageKeys(
        "running",
        [{ type: "tool_use", tool: "exec_command" } as never],
        "online",
        "reconnecting",
      ),
    ).toEqual({ stageKey: "reconnecting" });
  });

  it("a reconnecting hint does not override queued/offline lifecycle states", () => {
    // The agent isn't running yet — the lifecycle status wins over a stale hint.
    expect(pickStageKeys("queued", [], "online", "reconnecting")).toEqual({
      stageKey: "queued",
    });
  });
});
