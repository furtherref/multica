import type {
  RuntimeBudgetPeriod,
  RuntimeBudgetScope,
  RuntimeBudgetScopeInput,
  RuntimeCostBudget,
  RuntimeCostBudgetInput,
} from "@multica/core/types";

export const MAX_BUDGET_USD = 1_000_000;

// Fill ratio of a meter, 0..100. A reached period always fills the bar even
// when used_usd overshoots the limit.
export function budgetPercent(p: RuntimeBudgetPeriod | null): number {
  if (!p || p.limit_usd <= 0) return 0;
  if (p.reached === true) return 100;
  return Math.min(100, (p.used_usd / p.limit_usd) * 100);
}

export function scopeHasReached(s: RuntimeBudgetScope): boolean {
  return s.daily?.reached === true || s.weekly?.reached === true || s.monthly?.reached === true;
}

export function countReachedUsers(users: RuntimeBudgetScope[]): number {
  return users.filter(scopeHasReached).length;
}

function scopeToInput(s: RuntimeBudgetScope | null): RuntimeBudgetScopeInput {
  return {
    ...(s?.user_id ? { user_id: s.user_id } : {}),
    daily_usd: s?.daily?.limit_usd ?? null,
    weekly_usd: s?.weekly?.limit_usd ?? null,
    monthly_usd: s?.monthly?.limit_usd ?? null,
  };
}

// Editor seed: the runtime scope always exists as a row of inputs even when
// the server has no total row yet.
export function budgetToInput(b: RuntimeCostBudget): RuntimeCostBudgetInput {
  return {
    runtime: scopeToInput(b.runtime),
    users: (b.users ?? []).map(scopeToInput),
  };
}

// "" -> null (no limit); a valid positive amount with <= 2 decimals -> number;
// anything else -> undefined (invalid, block save).
export function parseBudgetField(raw: string): number | null | undefined {
  const trimmed = raw.trim();
  if (trimmed === "") return null;
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return undefined;
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value <= 0 || value > MAX_BUDGET_USD) return undefined;
  return value;
}

export function scopeIsEmpty(s: RuntimeBudgetScopeInput): boolean {
  return s.daily_usd === null && s.weekly_usd === null && s.monthly_usd === null;
}
