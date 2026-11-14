import { describe, it, expect } from "vitest";
import { initialLiveActivityState, liveActivityReducer } from "./live-activity";

describe("liveActivityReducer", () => {
  it("sets the hint from a task:activity action", () => {
    const s = liveActivityReducer(initialLiveActivityState, {
      type: "activity",
      value: "reconnecting",
      afterSeq: 0,
    });
    expect(s.activity).toBe("reconnecting");
    expect(s.activityAfterSeq).toBe(0);
  });

  it("a message with a strictly higher seq clears the hint", () => {
    let s = liveActivityReducer(initialLiveActivityState, {
      type: "activity",
      value: "reconnecting",
      afterSeq: 2,
    });
    s = liveActivityReducer(s, { type: "message", seq: 3 }); // 3 > 2 → supersedes
    expect(s.activity).toBeUndefined();
    expect(s.maxSeq).toBe(3);
  });

  it("a message at or below the hint's frontier does NOT clear it", () => {
    let s = liveActivityReducer(initialLiveActivityState, {
      type: "activity",
      value: "reconnecting",
      afterSeq: 5,
    });
    s = liveActivityReducer(s, { type: "message", seq: 5 }); // 5 > 5 is false
    expect(s.activity).toBe("reconnecting");
    s = liveActivityReducer(s, { type: "message", seq: 4 }); // older
    expect(s.activity).toBe("reconnecting");
  });

  it("drops a reordered (stale) activity whose frontier is behind a seen message", () => {
    let s = liveActivityReducer(initialLiveActivityState, { type: "message", seq: 5 });
    s = liveActivityReducer(s, { type: "activity", value: "reconnecting", afterSeq: 4 }); // 4 < 5 → ignore
    expect(s.activity).toBeUndefined();
  });

  it("accepts an activity whose frontier is at the seen message frontier", () => {
    let s = liveActivityReducer(initialLiveActivityState, { type: "message", seq: 5 });
    s = liveActivityReducer(s, { type: "activity", value: "reconnecting", afterSeq: 5 }); // 5 < 5 is false
    expect(s.activity).toBe("reconnecting");
  });
});
