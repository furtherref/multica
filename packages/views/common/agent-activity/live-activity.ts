// Pure reducer for the transient "what is this task doing right now" hint
// (e.g. reconnecting), driven by task:activity / task:message WS events.
//
// Kept pure — no React, no store — so it is unit-testable and reusable by the
// useLiveTaskActivity hook, which holds the state component-locally (the
// project forbids writing WS events into a store). It encodes the same seq
// guard the daemon/live-card use: a task:activity carries the message-seq
// frontier it was emitted at (after_seq); a later message supersedes the hint,
// and a reordered (late) activity is dropped if a newer message already landed.

export interface LiveActivityState {
  /** Current transient activity hint, or undefined when none/expired. */
  activity?: string;
  /** Seq frontier the current hint was emitted at. */
  activityAfterSeq?: number;
  /** Highest message seq seen — guards against a reordered (late) activity. */
  maxSeq: number;
}

export type LiveActivityAction =
  | { type: "activity"; value: string; afterSeq: number }
  | { type: "message"; seq: number };

export const initialLiveActivityState: LiveActivityState = { maxSeq: 0 };

export function liveActivityReducer(
  state: LiveActivityState,
  action: LiveActivityAction,
): LiveActivityState {
  switch (action.type) {
    case "activity":
      // Stale: a message at/past this frontier already arrived. The activity
      // event is async and can be reordered behind a later message report.
      if (action.afterSeq < state.maxSeq) return state;
      return {
        ...state,
        activity: action.value,
        activityAfterSeq: action.afterSeq,
      };
    case "message": {
      const maxSeq = Math.max(state.maxSeq, action.seq);
      // A message strictly newer than the hint supersedes it.
      const supersedes =
        state.activity !== undefined && action.seq > (state.activityAfterSeq ?? -1);
      return {
        maxSeq,
        activity: supersedes ? undefined : state.activity,
        activityAfterSeq: supersedes ? undefined : state.activityAfterSeq,
      };
    }
    default:
      return state;
  }
}
