"use client";

import { useCallback, useReducer } from "react";
import { useWSEvent } from "@multica/core/realtime";
import type { TaskActivityPayload, TaskMessagePayload } from "@multica/core/types/events";
import { initialLiveActivityState, liveActivityReducer } from "./live-activity";

// useLiveTaskActivity subscribes to the transient task:activity hint for a
// single task and holds it in component-local state (no store — the project
// forbids writing WS events into a store). It also watches task:message to
// expire a stale hint via the seq guard, but does NOT append messages to any
// timeline — surfaces that need live messages have their own path.
//
// Used as a fallback by surfaces that aren't fed a live `activity` prop by a
// persistent parent (e.g. a transcript dialog opened lazily from the execution
// log or the agent activity tab). Pass `undefined` to disable (e.g. terminal
// tasks). Returns the current activity string (e.g. "reconnecting") or
// undefined.
export function useLiveTaskActivity(taskId: string | undefined): string | undefined {
  const [state, dispatch] = useReducer(liveActivityReducer, initialLiveActivityState);

  useWSEvent(
    "task:activity",
    useCallback(
      (payload: unknown) => {
        const p = payload as TaskActivityPayload;
        if (taskId && p.task_id === taskId) {
          dispatch({ type: "activity", value: p.activity, afterSeq: p.after_seq ?? 0 });
        }
      },
      [taskId],
    ),
  );

  useWSEvent(
    "task:message",
    useCallback(
      (payload: unknown) => {
        const p = payload as TaskMessagePayload;
        if (taskId && p.task_id === taskId) {
          dispatch({ type: "message", seq: p.seq });
        }
      },
      [taskId],
    ),
  );

  return state.activity;
}
