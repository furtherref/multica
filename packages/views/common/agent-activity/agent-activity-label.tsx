"use client";

import { cn } from "@multica/ui/lib/utils";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import type { AgentAvailability } from "@multica/core/agents";
import type { TaskMessagePayload } from "@multica/core/types";
import { useT } from "../../i18n";
import { pickStageKeys } from "./pick-stage";

interface Props {
  /** Task lifecycle status (`running`, `dispatched`, `queued`, …). */
  status: string | undefined;
  /** Live task-message stream — the latest non-error entry picks the label. */
  taskMessages: readonly TaskMessagePayload[];
  /** Resolved agent presence; pass `undefined` to skip availability hints. */
  availability?: AgentAvailability | undefined;
  /** Transient hint from a `task:activity` event (e.g. "reconnecting"). */
  activity?: string;
  /** Suppress the built-in spinner when the caller renders its own. */
  hideSpinner?: boolean;
  className?: string;
}

// AgentActivityLabel renders the live "what is the agent doing" label —
// "Reading files", "Thinking", "Typing", etc. — derived from the streamed
// task messages. Shared by the chat status pill and the issue task panel so
// both surface the same fine-grained activity instead of a generic
// "working" banner that looks frozen between tool calls.
export function AgentActivityLabel({
  status,
  taskMessages,
  availability,
  activity,
  hideSpinner,
  className,
}: Props) {
  const { t } = useT("common");
  const decision = pickStageKeys(status, taskMessages, availability, activity);
  const label = decision.toolKey
    ? t(($) => $.status_pill.tools[decision.toolKey!])
    : t(($) => $.status_pill.stages[decision.stageKey]);
  // toolKey labels are always "actively working"; only explicit stage
  // decisions carry the static flag (queued / waiting / offline).
  const isStatic = decision.toolKey ? false : decision.static === true;

  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)}>
      {!hideSpinner && !isStatic && (
        <UnicodeSpinner name="breathe" className="opacity-70" />
      )}
      <span className={cn("truncate", !isStatic && "animate-chat-text-shimmer")}>
        {label}
      </span>
    </span>
  );
}
