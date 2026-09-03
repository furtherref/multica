// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type {
  MemberWithUser,
  RuntimeCostBudget,
  RuntimeCostBudgetInput,
} from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

// Typed vars so `mutateAsync.mock.calls[0][0]` is not an empty tuple.
const mutateAsync = vi.hoisted(() =>
  vi.fn(async (_vars: { runtimeId: string; input: RuntimeCostBudgetInput }) => ({})),
);
vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntimeCostBudget: () => ({ mutateAsync, isPending: false }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <div data-testid={`avatar-${actorId}`} />,
}));

import { RuntimeBudgetDialog } from "./runtime-budget-dialog";

const members = [
  { user_id: "u-zhang", name: "张强", role: "owner", email: "", avatar_url: null },
  { user_id: "u-li", name: "Li Wei", role: "member", email: "", avatar_url: null },
] as MemberWithUser[];

const period = (limit: number) => ({ limit_usd: limit, used_usd: 0, period_start: "", reset_at: "", reached: false });

const budget: RuntimeCostBudget = {
  runtime: { daily: period(60), weekly: period(300), monthly: null },
  users: [{ user_id: "u-zhang", daily: period(20), weekly: null, monthly: period(200) }],
  can_manage: true,
};

function wrap(ui: ReactNode) {
  return render(<I18nProvider locale="en" resources={TEST_RESOURCES}>{ui}</I18nProvider>);
}

describe("RuntimeBudgetDialog", () => {
  beforeEach(() => mutateAsync.mockClear());

  it("seeds inputs from the budget and saves the full replacement", async () => {
    const onOpenChange = vi.fn();
    wrap(<RuntimeBudgetDialog open onOpenChange={onOpenChange} runtimeId="rt-1" budget={budget} members={members} />);
    expect((screen.getByLabelText("Runtime total Daily") as HTMLInputElement).value).toBe("60");
    fireEvent.change(screen.getByLabelText("Runtime total Monthly"), { target: { value: "500" } });
    fireEvent.change(screen.getByLabelText("张强 Daily"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync).toHaveBeenCalledWith({
      runtimeId: "rt-1",
      input: {
        runtime: { daily_usd: 60, weekly_usd: 300, monthly_usd: 500 },
        users: [{ user_id: "u-zhang", daily_usd: null, weekly_usd: null, monthly_usd: 200 }],
      },
    });
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("drops a member row whose fields are all cleared and blocks invalid amounts", async () => {
    wrap(<RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={budget} members={members} />);
    // The seeded row carries daily 20 and monthly 200; both must go before the
    // row counts as empty. Test 1 above covers the partial clear that keeps it.
    fireEvent.change(screen.getByLabelText("张强 Daily"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("张强 Monthly"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Runtime total Daily"), { target: { value: "-5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByText("Enter a positive USD amount with at most two decimals.")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Runtime total Daily"), { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]?.[0]).toEqual({
      runtimeId: "rt-1",
      input: { runtime: { daily_usd: 5, weekly_usd: 300, monthly_usd: null }, users: [] },
    });
  });

  it("adds a member row from the picker", () => {
    wrap(<RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={budget} members={members} />);
    const trigger = screen.getByRole("button", { name: "Add member" });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(screen.getByRole("option", { name: "Li Wei" }));
    expect(screen.getByLabelText("Li Wei Daily")).toBeTruthy();
  });

  it("keeps in-progress edits when the budget refetches while the dialog stays open", () => {
    const { rerender } = wrap(
      <RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={budget} members={members} />,
    );
    fireEvent.change(screen.getByLabelText("Runtime total Daily"), { target: { value: "77" } });

    // A background refetch hands down a fresh object with the same limits but
    // moved `used_usd`. Re-seeding on that would discard the typed "77".
    const refetched: RuntimeCostBudget = {
      ...budget,
      runtime: {
        daily: { ...period(60), used_usd: 41.5 },
        weekly: { ...period(300), used_usd: 122.75 },
        monthly: null,
      },
    };
    rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <RuntimeBudgetDialog open onOpenChange={vi.fn()} runtimeId="rt-1" budget={refetched} members={members} />
      </I18nProvider>,
    );

    expect((screen.getByLabelText("Runtime total Daily") as HTMLInputElement).value).toBe("77");
  });

  it("closes only the member popover on Escape, leaving the dialog open", () => {
    const onOpenChange = vi.fn();
    wrap(<RuntimeBudgetDialog open onOpenChange={onOpenChange} runtimeId="rt-1" budget={budget} members={members} />);
    const trigger = screen.getByRole("button", { name: "Add member" });
    fireEvent.click(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    // Escape must originate inside the popup, where a real browser puts focus
    // once it opens. jsdom leaves `document.activeElement` on the trigger
    // because Base UI moves focus asynchronously, so target the search box.
    fireEvent.keyDown(screen.getByPlaceholderText("Search members"), { key: "Escape" });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(onOpenChange).not.toHaveBeenCalled();
  });
});
