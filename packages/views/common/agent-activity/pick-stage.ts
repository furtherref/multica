import type { AgentAvailability } from "@multica/core/agents";
import type { TaskMessagePayload } from "@multica/core/types";

// Live "stage" of an active agent task, surfaced both in the chat status pill
// and the issue task panel. Pure decision logic — it maps a task's status +
// latest streamed message into a translation key, with no React or i18n
// coupling, so it can be unit-tested in isolation and reused across views.

export type StageKey =
  | "offline"
  | "reconnecting"
  | "queued"
  | "waiting_local_directory"
  | "starting_up"
  | "thinking"
  | "typing";

export type ToolKey =
  | "running_command"
  | "reading_files"
  | "searching_code"
  | "making_edits"
  | "searching_web"
  | "fallback";

// Tool slug → translation key. Unknown tools fall back to "Working".
//
// Tool names are runtime-specific and arrive lowercased. Claude Code emits
// bash/read/glob/grep/write/edit/…; the Codex backend emits "exec_command"
// and "patch_apply" (server/pkg/agent/codex.go) — the two most common Codex
// tools, so they MUST map here or the live-stage label degrades to the
// generic "Working" fallback for exactly the runtime this work targets.
// Aliases cover plausible spellings across the other runtimes.
export const TOOL_KEY_BY_SLUG: Record<string, Exclude<ToolKey, "fallback">> = {
  // Command execution
  bash: "running_command",
  exec: "running_command",
  exec_command: "running_command",
  "exec-command": "running_command",
  shell: "running_command",
  // Reading
  read: "reading_files",
  glob: "reading_files",
  // Searching code
  grep: "searching_code",
  // Editing files
  write: "making_edits",
  edit: "making_edits",
  multi_edit: "making_edits",
  multiedit: "making_edits",
  patch_apply: "making_edits",
  apply_patch: "making_edits",
  file_edit: "making_edits",
  // Web search
  web_search: "searching_web",
  websearch: "searching_web",
};

// Pure stage decision returning translation keys. Callers map these keys into
// localized labels — keeping the decision pure makes the priority rules easy
// to follow without translation noise.
export function pickStageKeys(
  status: string | undefined,
  taskMessages: readonly TaskMessagePayload[],
  availability: AgentAvailability | undefined,
  // Transient activity hint from a `task:activity` event (e.g. "reconnecting").
  // Highest-priority signal while running — overrides the message-derived stage.
  activity?: string,
): { stageKey: StageKey; toolKey?: ToolKey; static?: boolean } {
  if (
    (status === "queued" || status === "dispatched") &&
    availability === "offline"
  ) {
    return { stageKey: "offline", static: true };
  }
  if (
    (status === "queued" || status === "dispatched") &&
    availability === "unstable"
  ) {
    return { stageKey: "reconnecting" };
  }
  // Daemon-emitted hold state for the local_directory flow: the project is
  // pinned to a path that another task currently owns. The daemon publishes
  // this status string when it dequeues a task but can't acquire the path
  // lock; the renderer surfaces a dedicated label so the user understands
  // why a queued task isn't moving.
  if (status === "waiting_local_directory") {
    return { stageKey: "waiting_local_directory", static: true };
  }
  if (status === "queued") return { stageKey: "queued" };
  if (status === "dispatched") return { stageKey: "starting_up" };

  // running: a live "reconnecting" hint is the most current signal — surface
  // it over the message-derived stage so the panel reads "Reconnecting"
  // instead of a stale "Running a command" while nothing is progressing.
  if (activity === "reconnecting") return { stageKey: "reconnecting" };

  // Otherwise the latest meaningful message decides the label.
  let latest: TaskMessagePayload | null = null;
  for (let i = taskMessages.length - 1; i >= 0; i--) {
    const m = taskMessages[i];
    if (m && m.type !== "error" && m.type !== "tool_result") {
      latest = m;
      break;
    }
  }

  if (!latest) return { stageKey: "thinking" };
  if (latest.type === "thinking") return { stageKey: "thinking" };
  if (latest.type === "text") return { stageKey: "typing" };
  if (latest.type === "tool_use") {
    const tool = (latest.tool ?? "").toLowerCase();
    const toolKey = TOOL_KEY_BY_SLUG[tool] ?? "fallback";
    // tool_use is technically still "thinking + tool" — surface the tool
    // label in the toolKey channel; main stage label uses the tool one.
    return { stageKey: "thinking", toolKey };
  }
  return { stageKey: "thinking" };
}
