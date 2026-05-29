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

  // Effective status — defense-in-depth derive on top of the cache. If any
  // task_message has streamed in, the daemon has by definition started
  // running; we trust that observation over a stale cache.
  const status = taskMessages.length > 0 ? "running" : pendingTask.status;
  const elapsedSecs = Math.max(0, Math.floor((now - anchor) / 1000));

  return (
    <div
      className="flex items-center gap-1.5 px-1 text-xs text-muted-foreground"
      aria-live="polite"
    >
      <AgentActivityLabel
        status={status}
        taskMessages={taskMessages}
        availability={availability}
      />
      <span className="opacity-70 shrink-0">
        · {formatElapsedSecs(elapsedSecs)}
      </span>
    </div>
  );
}
