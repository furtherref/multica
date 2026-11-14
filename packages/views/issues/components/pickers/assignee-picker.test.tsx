import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enIssues from "../../../locales/en/issues.json";
import { PillButton } from "../../../common/pill-button";

const TEST_RESOURCES = {
  en: { issues: enIssues },
};

const mockAuthState = { user: { id: "user-1" } };

const memberQueryFn = vi.hoisted(() => vi.fn(async () => []));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: typeof mockAuthState) => unknown) =>
      selector ? selector(mockAuthState) : mockAuthState,
    { getState: () => mockAuthState },
  ),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Someone" }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: (wsId: string) => ({
    queryKey: ["members", wsId],
    queryFn: memberQueryFn,
  }),
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: async () => [],
  }),
  squadListOptions: (wsId: string) => ({
    queryKey: ["squads", wsId],
    queryFn: async () => [],
  }),
  assigneeFrequencyOptions: (wsId: string) => ({
    queryKey: ["assignee-frequency", wsId],
    queryFn: async () => [],
  }),
}));

vi.mock("../../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

import { AssigneePicker } from "./assignee-picker";

function renderPicker(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </I18nProvider>,
  );
}

describe("AssigneePicker trigger content", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // The create-issue dialog passes a bare childless PillButton as
  // triggerRender and relies on the picker's computed default content
  // ("Unassigned" / avatar + name). The deferred lookalike trigger can't
  // compute that content, so such callers must render the real picker
  // eagerly.
  it("shows Unassigned in a childless triggerRender without interaction", () => {
    renderPicker(
      <AssigneePicker
        assigneeType={null}
        assigneeId={null}
        onUpdate={() => {}}
        triggerRender={<PillButton />}
        align="start"
      />,
    );

    expect(screen.getByText("Unassigned")).toBeInTheDocument();
  });

  // Board cards / list rows bring their own trigger content; those callers
  // must keep the deferred path (no query subscriptions before first
  // interaction) — that's the perf contract the deferral exists for.
  it("defers mounting (no queries) when the caller brings trigger content", () => {
    renderPicker(
      <AssigneePicker
        assigneeType={null}
        assigneeId={null}
        onUpdate={() => {}}
        trigger={<span>own content</span>}
      />,
    );

    expect(screen.getByText("own content")).toBeInTheDocument();
    expect(memberQueryFn).not.toHaveBeenCalled();
  });
});
