// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  budgetPercent,
  budgetToInput,
  countReachedUsers,
  parseBudgetField,
  scopeIsEmpty,
} from "./budget";

const period = (limit: number, used: number, reached = used >= limit) => ({
  limit_usd: limit,
  used_usd: used,
  period_start: "2026-09-02T00:00:00Z",
  reset_at: "2026-09-03T00:00:00Z",
  reached,
});

describe("budgetPercent", () => {
  it("is 0 for an unlimited period and clamps to 100", () => {
    expect(budgetPercent(null)).toBe(0);
    expect(budgetPercent(period(20, 3.42))).toBeCloseTo(17.1);
    expect(budgetPercent(period(20, 20.31))).toBe(100);
    expect(budgetPercent(period(0, 5))).toBe(0);
  });
});

describe("countReachedUsers", () => {
  it("counts users with any reached period once", () => {
    expect(
      countReachedUsers([
        { user_id: "a", daily: period(20, 20.31), weekly: period(50, 60), monthly: null },
        { user_id: "b", daily: period(10, 4.85), weekly: null, monthly: null },
      ]),
    ).toBe(1);
  });
});

describe("budgetToInput / scopeIsEmpty / parseBudgetField", () => {
  it("maps limits to *_usd inputs and null scopes to empty inputs", () => {
    expect(
      budgetToInput({
        runtime: { daily: period(20, 1), weekly: null, monthly: null },
        users: [{ user_id: "a", daily: null, weekly: period(50, 1), monthly: null }],
        can_manage: true,
      }),
    ).toEqual({
      runtime: { daily_usd: 20, weekly_usd: null, monthly_usd: null },
      users: [{ user_id: "a", daily_usd: null, weekly_usd: 50, monthly_usd: null }],
    });
    expect(budgetToInput({ runtime: null, users: [], can_manage: false })).toEqual({
      runtime: { daily_usd: null, weekly_usd: null, monthly_usd: null },
      users: [],
    });
  });

  it("treats blank as no limit and rejects non-positive or >2-decimal input", () => {
    expect(parseBudgetField("")).toBeNull();
    expect(parseBudgetField("  ")).toBeNull();
    expect(parseBudgetField("20")).toBe(20);
    expect(parseBudgetField("0.07")).toBe(0.07);
    expect(parseBudgetField("0")).toBeUndefined();
    expect(parseBudgetField("-1")).toBeUndefined();
    expect(parseBudgetField("1.005")).toBeUndefined();
    expect(parseBudgetField("abc")).toBeUndefined();
    expect(parseBudgetField("1000001")).toBeUndefined();
  });

  it("scopeIsEmpty is true only when all three limits are null", () => {
    expect(scopeIsEmpty({ daily_usd: null, weekly_usd: null, monthly_usd: null })).toBe(true);
    expect(scopeIsEmpty({ daily_usd: 1, weekly_usd: null, monthly_usd: null })).toBe(false);
  });
});
