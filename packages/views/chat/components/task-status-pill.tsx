"use client";

import { useEffect, useRef, useState } from "react";
import type { AgentAvailability } from "@multica/core/agents";
import type { ChatPendingTask, TaskMessagePayload } from "@multica/core/types";
import { AgentActivityLabel } from "../../common/agent-activity";
import { formatElapsedSecs } from "../lib/format";

interface Props {
  /** Server-authoritative pending-task snapshot (`created_at` anchors the timer). */
  pendingTask: ChatPendingTask;
  /** Live task-message stream — the latest non-error entry decides the running-stage label. */
  taskMessages: readonly TaskMessagePayload[];
  /** Resolved presence; pass `undefined` to suppress availability hints. */
  availability: AgentAvailability | undefined;
}

// A deferred chat task is an older turn waiting for its retry backoff, not
// active model work. Keep it authoritative over the stream-derived "running"
// status so the pill never regresses to a misleading running label.
export function effectiveTaskStatus(
  status: string | undefined,
  taskMessages: readonly TaskMessagePayload[],
): string | undefined {
  if (status === "deferred") return status;
  return taskMessages.length > 0 ? "running" : status;
}

export function TaskStatusPill({
  pendingTask,
  taskMessages,
  availability,
}: Props) {
  // Anchor: locked on first render. Once set we never reassign — otherwise
  // the timer would visibly snap backwards when an optimistic-seeded
  // `Date.now()` anchor is later replaced by a server-side created_at that
  // happened a few hundred ms earlier. Monotonic elapsed > strict accuracy.
  const anchorRef = useRef<number | null>(null);
  if (anchorRef.current === null) {
    if (pendingTask.created_at) {
      const t = Date.parse(pendingTask.created_at);
      anchorRef.current = Number.isFinite(t) ? t : Date.now();
    } else {
      anchorRef.current = Date.now();
    }
  }
  const anchor = anchorRef.current;

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  // Effective status — streamed messages prove a task has started, except
  // when the server has since moved that same task into retry backoff.
  const status = effectiveTaskStatus(pendingTask.status, taskMessages);
  const elapsedSecs = Math.max(0, Math.floor((now - anchor) / 1000));

  return (
    <div
      className="flex items-center gap-1.5 px-1 text-caption text-muted-foreground"
      aria-live="polite"
    >
      <AgentActivityLabel
        status={status}
        taskMessages={taskMessages}
        availability={availability}
      />
      <span className="opacity-70 shrink-0 tabular-nums">
        · {formatElapsedSecs(elapsedSecs)}
      </span>
    </div>
  );
}
