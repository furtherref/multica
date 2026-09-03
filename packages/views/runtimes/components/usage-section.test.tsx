// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

// The viewer's tz (Viewing layer) drives both the trend and the heatmap.
const VIEWER_TZ = "Asia/Tokyo";

// runtimeUsageOptions is the trend-fetch query. Capture its args so the
// test can assert which tz the trend was wired with.
const runtimeUsageOptions = vi.hoisted(() =>
  vi.fn((..._args: unknown[]) => ({ kind: "usage" as const })),
);
const runtimeUsageByAgentOptions = vi.hoisted(() =>
  vi.fn((..._args: unknown[]) => ({ kind: "by-agent" as const })),
);
const runtimeUsageCoverageOptions = vi.hoisted(() =>
  vi.fn((..._args: unknown[]) => ({ kind: "coverage" as const })),
);

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => VIEWER_TZ,
}));

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeUsageOptions,
  runtimeUsageByAgentOptions,
  runtimeUsageCoverageOptions,
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ kind: "agents" as const }),
  memberListOptions: () => ({ kind: "members" as const }),
}));

// The real ActorAvatar pulls navigation/paths providers the usage section
// doesn't otherwise need; stub it to an inspectable marker.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: { actorType: string; actorId: string }) => (
    <div data-testid={`avatar-${actorType}-${actorId}`} />
  ),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// custom-pricing-store is consumed two ways: usage-section reads the store
// hook, and runtimes/utils reads getCustomPricing(). The hook must be both
// callable and expose getState(), mirroring a real Zustand store. Backed by
// a mutable holder so a test can seed saved overrides — with a hard-coded
// empty store, `collectUnmappedModels` can never see an override and the
// "saved rates stay editable" path below would be untestable.
const pricingState = vi.hoisted(() => ({
  pricings: {} as Record<string, unknown>,
}));

vi.mock("@multica/core/runtimes/custom-pricing-store", () => {
  const useCustomPricingStore = Object.assign(
    (sel?: (s: typeof pricingState) => unknown) =>
      sel ? sel(pricingState) : pricingState,
    { getState: () => pricingState },
  );
  return {
    useCustomPricingStore,
    getCustomPricing: (model: string) => pricingState.pricings[model],
  };
});

// Lets a test swap in its own usage rows (e.g. an unpriced model) without
// re-mocking the whole query layer. `null` keeps the default fixture.
const usageOverride = vi.hoisted(() => ({ rows: null as unknown[] | null }));
const coverageOverride = vi.hoisted(() => ({
  rows: null as unknown[] | null,
  error: false,
}));

// useQuery is mocked so the component renders synchronously with canned
// data — the `kind` tag on each query-options object routes the response.
vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  const dateDaysAgo = (days: number) => {
    const date = new Date();
    date.setUTCDate(date.getUTCDate() - days);
    return date.toISOString().slice(0, 10);
  };
  const usageRows = [
    {
      runtime_id: "r-1",
      date: dateDaysAgo(0),
      provider: "anthropic",
      model: "claude-sonnet-4-6",
      input_tokens: 1_000,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
    },
    {
      runtime_id: "r-1",
      date: dateDaysAgo(15),
      provider: "anthropic",
      model: "claude-sonnet-4-6",
      input_tokens: 2_000,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
    },
  ];
  const coverageRows = [
    {
      date: dateDaysAgo(0),
      completed_runs: 1,
      complete_runs: 1,
      output_only_runs: 0,
      missing_runs: 0,
    },
  ];
  const byAgentTokens = {
    provider: "anthropic",
    model: "claude-sonnet-4-6",
    input_tokens: 1_000_000,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    task_count: 1,
  };
  // a-1/a-2 share owner u-1, a-3 is ownerless, a-gone is a deleted agent
  // (absent from the agent list) — together they exercise the fold, the
  // no-owner bucket, and the deleted-agent bucket.
  const byAgentRows = [
    { agent_id: "a-1", ...byAgentTokens },
    { agent_id: "a-2", ...byAgentTokens },
    { agent_id: "a-3", ...byAgentTokens },
    { agent_id: "a-gone", ...byAgentTokens },
  ];
  const agents = [
    { id: "a-1", name: "Agent One", owner_id: "u-1" },
    { id: "a-2", name: "Agent Two", owner_id: "u-1" },
    { id: "a-3", name: "Agent Three", owner_id: null },
  ];
  const members = [
    { user_id: "u-1", role: "member", name: "Alice Zhang", email: "", avatar_url: null },
  ];
  const dataByKind: Record<string, unknown> = {
    usage: usageRows,
    coverage: coverageRows,
    "by-agent": byAgentRows,
    agents,
    members,
  };
  return {
    ...actual,
    useQuery: (opts: { kind?: string }) => ({
      data:
        opts?.kind === "usage"
          ? (usageOverride.rows ?? usageRows)
          : opts?.kind === "coverage"
            ? (coverageOverride.rows ?? coverageRows)
          : (opts?.kind && dataByKind[opts.kind]) || [],
      isLoading: false,
      isError: opts?.kind === "coverage" && coverageOverride.error,
    }),
  };
});

// Charts are recharts-heavy; stub them. ActivityHeatmap echoes its `tz`
// prop so the test can read which tz the heatmap was wired with.
vi.mock("./charts", () => ({
  DailyCostChart: () => <div data-testid="daily-cost-chart" />,
  DailyTokensChart: () => <div data-testid="daily-tokens-chart" />,
  WeeklyCostChart: () => <div data-testid="weekly-cost-chart" />,
  WeeklyTokensChart: () => <div data-testid="weekly-tokens-chart" />,
  ActivityHeatmap: ({ tz }: { tz: string }) => (
    <div data-testid="heatmap-tz">{tz}</div>
  ),
}));

vi.mock("./custom-pricing-dialog", () => ({
  CustomPricingDialog: () => null,
}));

import { UsageSection } from "./usage-section";

const RUNTIME: AgentRuntime = {
  id: "r-1",
  workspace_id: "ws-1",
  daemon_id: null,
  name: "test-runtime",
  runtime_mode: "cloud",
  provider: "claude",
  launch_header: "",
  status: "online",
  device_info: "",
  metadata: {},
  owner_id: null,
  visibility: "private",
  last_seen_at: null,
  created_at: "2026-05-01T00:00:00Z",
  updated_at: "2026-05-01T00:00:00Z",
};

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("UsageSection — Viewing timezone wiring", () => {
  beforeEach(() => {
    runtimeUsageOptions.mockClear();
    runtimeUsageByAgentOptions.mockClear();
  });

  it("fetches the trend in the viewer's tz", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(runtimeUsageOptions).toHaveBeenCalled();
    const [, days, tz] = runtimeUsageOptions.mock.calls[0]!;
    expect(days).toBe(180);
    expect(tz).toBe(VIEWER_TZ);
  });

  it("renders the heatmap in the viewer's tz", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    // The heatmap is an opt-in toggle inside the "When" card.
    fireEvent.click(screen.getByRole("button", { name: "Heatmap" }));

    expect(screen.getByTestId("heatmap-tz").textContent).toBe(VIEWER_TZ);
  });

  it("renders KPI values with NumberFlow and updates them when the period changes", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    const flows = Array.from(document.querySelectorAll("number-flow-react"));
    expect(flows).toHaveLength(3);
    expect(flows.at(-1)).toHaveAttribute("aria-label", "3K");
    expect(
      flows.every(
        (flow) =>
          (flow as HTMLElement & { respectMotionPreference?: boolean })
            .respectMotionPreference === true,
      ),
    ).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "7d" }));

    expect(flows.at(-1)).toHaveAttribute("aria-label", "1K");
  });
});

describe("UsageSection — daily breakdown", () => {
  beforeEach(() => {
    usageOverride.rows = [
      {
        runtime_id: "r-1",
        date: new Date().toISOString().slice(0, 10),
        provider: "copilot",
        model: "gpt-5.5",
        input_tokens: 34_300,
        output_tokens: 3_000,
        cache_read_tokens: 175_700,
        cache_write_tokens: 0,
        cost_usd_ticks: 3_500_000_000,
        uncosted_input_tokens: 0,
        uncosted_output_tokens: 0,
        uncosted_cache_read_tokens: 0,
        uncosted_cache_write_tokens: 0,
      },
    ];
    coverageOverride.rows = null;
    coverageOverride.error = false;
    pricingState.pricings = {};
  });

  it("shows the calculated cost as the final daily breakdown column", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    const toggle = screen.getByRole("button", { name: "Daily breakdown table" });
    fireEvent.click(toggle);
    const breakdown = within(toggle.parentElement!);

    const costHeader = breakdown.getByText("Cost");
    const costCell = breakdown.getByText("$0.35");

    expect(costHeader.parentElement?.lastElementChild).toBe(costHeader);
    expect(costCell.parentElement?.lastElementChild).toBe(costCell);
  });
});

describe("CostByBlock — By owner tab", () => {
  it("defaults to By owner and orders the tabs owner → agent → model", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.getByText("Cost by owner")).toBeInTheDocument();

    const owner = screen.getByRole("button", { name: "By owner" });
    const agent = screen.getByRole("button", { name: "By agent" });
    const model = screen.getByRole("button", { name: "By model" });
    expect(
      owner.compareDocumentPosition(agent) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      agent.compareDocumentPosition(model) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("renders owner rows with member identity and a No-owner bucket", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    // u-1 owns two agents → a single folded row with the member avatar + name.
    expect(screen.getByTestId("avatar-member-u-1")).toBeInTheDocument();
    expect(screen.getByText("Alice Zhang")).toBeInTheDocument();
    // The ownerless agent and the deleted agent fold into one bucket row.
    expect(screen.getByText("No owner")).toBeInTheDocument();
    // Caption counts owner buckets: Alice + No owner.
    expect(screen.getByText("2 owners on this runtime")).toBeInTheDocument();
  });

  it("expands an owner row into its per-model breakdown and collapses it again", () => {
    // Canonical fold matrix lives in ../utils.test.ts (aggregateCostByOwnerModel);
    // this only covers the toggle wiring.
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    // Collapsed by default: no model rows under any owner.
    expect(screen.queryByText("claude-sonnet-4-6")).not.toBeInTheDocument();

    const toggles = screen.getAllByRole("button", { name: "Model breakdown" });
    // One toggle per owner bucket (Alice + No owner).
    expect(toggles).toHaveLength(2);
    expect(toggles[0]).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(toggles[0]!);

    expect(toggles[0]).toHaveAttribute("aria-expanded", "true");
    // Alice's two agents both used sonnet at 1M input → one model row,
    // 2M tokens, $6.00 (matches her owner row total).
    const modelRow = screen.getByText("claude-sonnet-4-6").closest("[data-model-row]");
    expect(modelRow).not.toBeNull();
    expect(within(modelRow as HTMLElement).getByText("2M")).toBeInTheDocument();
    expect(within(modelRow as HTMLElement).getByText("$6.00")).toBeInTheDocument();
    // Only the clicked owner expanded.
    expect(screen.getAllByText("claude-sonnet-4-6")).toHaveLength(1);

    fireEvent.click(toggles[0]!);

    expect(screen.queryByText("claude-sonnet-4-6")).not.toBeInTheDocument();
  });

  it("switches to the By agent tab on click", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    fireEvent.click(screen.getByRole("button", { name: "By agent" }));

    expect(screen.getByText("Cost by agent")).toBeInTheDocument();
    expect(screen.getByText("4 agents on this runtime")).toBeInTheDocument();
    expect(screen.getByTestId("avatar-agent-a-1")).toBeInTheDocument();
  });
});

describe("UsageSection — custom-pricing entry point", () => {
  // A model that no maintained row prices, so it lands in the unmapped
  // diagnostic. `collectUnmappedModels` keys it by provider, so the saved
  // override below must use the same `acme/…` key the dialog would store.
  const UNPRICED_KEY = "acme/made-up-model-9";
  const unpricedRows = [
    {
      runtime_id: "r-1",
      date: new Date().toISOString().slice(0, 10),
      provider: "acme",
      model: "made-up-model-9",
      input_tokens: 1_000,
      output_tokens: 500,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
    },
  ];

  beforeEach(() => {
    usageOverride.rows = null;
    coverageOverride.rows = null;
    coverageOverride.error = false;
    pricingState.pricings = {};
  });

  it("stays hidden when every model resolves and nothing is overridden", () => {
    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(
      screen.queryByRole("button", { name: "Set custom prices" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit custom prices" }),
    ).toBeNull();
  });

  it("warns and offers the dialog while a model is unpriced", () => {
    usageOverride.rows = unpricedRows;

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.getByRole("alert")).toHaveTextContent(UNPRICED_KEY);
    expect(
      screen.getByRole("button", { name: "Set custom prices" }),
    ).toBeInTheDocument();
  });

  it("keeps the dialog reachable after the last override is saved", () => {
    // Regression: a saved override makes the model resolve, so the window
    // has nothing unmapped left. Gating the bar on "something is unmapped"
    // used to remove the only entry point here, stranding the user with
    // rates they could no longer edit or delete.
    usageOverride.rows = unpricedRows;
    pricingState.pricings = {
      [UNPRICED_KEY]: { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 1 },
    };

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.queryByRole("alert")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Edit custom prices" }),
    ).toBeInTheDocument();
  });

  it("does not claim an unused saved custom price is active", () => {
    pricingState.pricings = {
      "unused/model": { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 1 },
    };

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(
      screen.getByText("Saved custom prices are not used in this period."),
    ).toBeInTheDocument();
  });
});

describe("UsageSection — telemetry coverage", () => {
  beforeEach(() => {
    usageOverride.rows = null;
    coverageOverride.rows = null;
    coverageOverride.error = false;
    pricingState.pricings = {};
  });

  it("marks output-only cost and input/cache as incomplete", () => {
    usageOverride.rows = [
      {
        runtime_id: "r-1",
        date: new Date().toISOString().slice(0, 10),
        provider: "copilot",
        model: "gpt-5.5",
        input_tokens: 0,
        output_tokens: 1_000,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
      },
    ];
    coverageOverride.rows = [
      {
        date: new Date().toISOString().slice(0, 10),
        completed_runs: 1,
        complete_runs: 0,
        output_only_runs: 1,
        missing_runs: 0,
      },
    ];

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "1 output-only, 0 missing",
    );
    expect(screen.getByText("Recorded cost lower bound · 30D")).toBeInTheDocument();
    expect(screen.getByLabelText("At least $0.03")).toBeInTheDocument();
    expect(
      screen.getByText("Input/cache incomplete · output 1K"),
    ).toBeInTheDocument();
  });

  it("keeps the no-usage page when the only incomplete runs fall outside the window", () => {
    const outsideWindow = new Date();
    outsideWindow.setUTCDate(outsideWindow.getUTCDate() - 60);
    usageOverride.rows = [];
    coverageOverride.rows = [
      {
        date: outsideWindow.toISOString().slice(0, 10),
        completed_runs: 2,
        complete_runs: 0,
        output_only_runs: 0,
        missing_runs: 2,
      },
    ];

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.getByText("No usage data yet")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("says so when coverage could not be loaded instead of implying complete telemetry", () => {
    coverageOverride.rows = [];
    coverageOverride.error = true;

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(
      screen.getByText(/Telemetry coverage could not be loaded/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("renders missing-only coverage instead of the generic no-usage page", () => {
    usageOverride.rows = [];
    coverageOverride.rows = [
      {
        date: new Date().toISOString().slice(0, 10),
        completed_runs: 2,
        complete_runs: 0,
        output_only_runs: 0,
        missing_runs: 2,
      },
    ];

    render(<UsageSection runtime={RUNTIME} />, { wrapper: Wrapper });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "0 output-only, 2 missing",
    );
    expect(screen.queryByText("No usage data yet")).toBeNull();
    expect(screen.getByText("Recorded cost lower bound · 30D")).toBeInTheDocument();
  });
});
