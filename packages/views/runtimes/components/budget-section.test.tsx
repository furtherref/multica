// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { AgentRuntime, RuntimeCostBudget } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

const budgetState = vi.hoisted(() => ({
  data: undefined as RuntimeCostBudget | undefined,
  role: "member" as "owner" | "admin" | "member",
  isLoading: false,
  isError: false,
}));

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeCostBudgetOptions: (rid: string) => ({ queryKey: ["runtimes", "budget", rid] }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    if (opts.queryKey[1] === "budget") {
      return {
        data: budgetState.data,
        isLoading: budgetState.isLoading,
        isError: budgetState.isError,
      };
    }
    return {
      data: [
        { user_id: "u-zhang", name: "张强", role: "owner", email: "", avatar_url: null },
        { user_id: "u-li", name: "Li Wei", role: "member", email: "", avatar_url: null },
      ],
      isLoading: false,
      isError: false,
    };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  useMutation: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@multica/core/permissions/use-current-member", () => ({
  useCurrentMember: () => ({ userId: "u-zhang", role: budgetState.role, member: null, isLoading: false }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <div data-testid={`avatar-${actorId}`} />,
}));

import { BudgetSection } from "./budget-section";

const runtime = { id: "rt-1", workspace_id: "ws-1", owner_id: "u-zhang" } as AgentRuntime;

const period = (limit: number, used: number) => ({
  limit_usd: limit, used_usd: used,
  period_start: "2026-09-02T00:00:00Z", reset_at: "2026-09-03T00:00:00Z",
  reached: used >= limit,
});

function wrap(ui: ReactNode) {
  return render(<I18nProvider locale="en" resources={TEST_RESOURCES}>{ui}</I18nProvider>);
}

describe("BudgetSection", () => {
  beforeEach(() => {
    budgetState.data = undefined;
    budgetState.role = "member";
    budgetState.isLoading = false;
    budgetState.isError = false;
  });

  it("shows a skeleton instead of the empty state while the budget is loading", () => {
    budgetState.isLoading = true;
    const { container } = wrap(<BudgetSection runtime={runtime} />);
    expect(screen.queryByText("No limits set")).toBeNull();
    expect(container.querySelectorAll('[data-slot="skeleton"]').length).toBeGreaterThan(0);
  });

  it("shows a load error instead of the empty state when the budget query fails", () => {
    budgetState.isError = true;
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.queryByText("No limits set")).toBeNull();
    expect(screen.getByText("Could not load the budget.")).toBeTruthy();
  });

  it("renders the empty state and hides the edit button for members", () => {
    budgetState.data = { runtime: null, users: [], can_manage: false };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByText("No limits set")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /budget/i })).toBeNull();
  });

  it("shows Set budget for admins on the empty state", () => {
    budgetState.role = "admin";
    budgetState.data = { runtime: null, users: [], can_manage: true };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByRole("button", { name: "Set budget" })).toBeTruthy();
  });

  it("is collapsed by default, shows the reached badge, and expands member rows", () => {
    budgetState.data = {
      runtime: { daily: period(60, 31.6), weekly: period(300, 118.4), monthly: null },
      users: [
        { user_id: "u-zhang", daily: period(20, 20.31), weekly: null, monthly: period(200, 64.1) },
        { user_id: "u-li", daily: period(10, 4.85), weekly: period(50, 22.9), monthly: null },
      ],
      can_manage: false,
    };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.getByText("Runtime total")).toBeTruthy();
    expect(screen.queryByText("Li Wei")).toBeNull();
    const toggle = screen.getByRole("button", { name: /Show 2 member budgets/ });
    expect(toggle.textContent).toContain("1 limit reached");
    expect(screen.getAllByText("Unlimited").length).toBe(1);

    fireEvent.click(toggle);
    expect(screen.getByText("Li Wei")).toBeTruthy();
    expect(screen.getByText("张强")).toBeTruthy();
    expect(screen.getByText("Limit reached")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Hide member budgets" })).toBeTruthy();
  });

  it("omits the toggle when there are no member rows", () => {
    budgetState.data = { runtime: { daily: period(60, 1), weekly: null, monthly: null }, users: [], can_manage: false };
    wrap(<BudgetSection runtime={runtime} />);
    expect(screen.queryByRole("button", { name: /member budget/ })).toBeNull();
  });
});
