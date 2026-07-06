// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockGetAgentEnv = vi.hoisted(() => vi.fn());
const mockUpdateAgentEnv = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: {
    getAgentEnv: (...args: unknown[]) => mockGetAgentEnv(...args),
    updateAgentEnv: (...args: unknown[]) => mockUpdateAgentEnv(...args),
  },
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

import { EnvTab } from "./env-tab";

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  custom_env_key_count: 1,
  created_at: "2026-06-12T00:00:00Z",
  updated_at: "2026-06-12T00:00:00Z",
  archived_at: null,
  archived_by: null,
  permission_mode: "public_to",
  invocation_targets: [],
} as Agent;

function envTabUi(canEdit: boolean | null) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <EnvTab agent={agent} canEdit={canEdit} />
    </I18nProvider>
  );
}

function renderEnvTab(canEdit: boolean | null) {
  return render(envTabUi(canEdit));
}

describe("EnvTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("hides the reveal button and shows a permission hint when canEdit is false", () => {
    renderEnvTab(false);

    expect(
      screen.queryByRole("button", { name: /reveal & edit/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/only the agent owner or a workspace owner\/admin/i),
    ).toBeInTheDocument();
    // The configured-key count stays visible to everyone.
    expect(screen.getByText(/1 variable configured/i)).toBeInTheDocument();
  });

  it("shows the reveal button and fetches env on click when canEdit is true", async () => {
    mockGetAgentEnv.mockResolvedValue({
      agent_id: "agent-1",
      custom_env: { FOO: "bar" },
    });
    renderEnvTab(true);

    const button = screen.getByRole("button", { name: /reveal & edit/i });
    fireEvent.click(button);

    await waitFor(() => expect(mockGetAgentEnv).toHaveBeenCalledWith("agent-1"));
    expect(await screen.findByDisplayValue("FOO")).toBeInTheDocument();
  });

  it("shows neutral copy with no reveal button while permission is unknown (canEdit null)", () => {
    renderEnvTab(null);

    expect(
      screen.queryByRole("button", { name: /reveal & edit/i }),
    ).not.toBeInTheDocument();
    // Loading must never be presented as a hard denial.
    expect(
      screen.queryByText(/only the agent owner or a workspace owner\/admin/i),
    ).not.toBeInTheDocument();
    // The configured-key count stays visible.
    expect(screen.getByText(/1 variable configured/i)).toBeInTheDocument();
  });

  it("unmounts the editor when canEdit flips to false after a reveal", async () => {
    mockGetAgentEnv.mockResolvedValue({
      agent_id: "agent-1",
      custom_env: { FOO: "bar" },
    });
    const { rerender } = renderEnvTab(true);

    fireEvent.click(screen.getByRole("button", { name: /reveal & edit/i }));
    expect(await screen.findByDisplayValue("FOO")).toBeInTheDocument();

    // Mid-session permission loss: ownership reassigned / role downgraded.
    rerender(envTabUi(false));

    await waitFor(() =>
      expect(screen.queryByDisplayValue("FOO")).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("button", { name: /save/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/only the agent owner or a workspace owner\/admin/i),
    ).toBeInTheDocument();
  });

  it("drops a reveal response that lands after canEdit stops being true mid-flight", async () => {
    // canEdit goes true -> null (permission re-resolving, e.g. the member
    // list refetches) while the reveal request is in flight. The post-reveal
    // cleanup effect only fires on an explicit `false`, so for the `null`
    // case the guard inside handleReveal is the ONLY thing stopping the late
    // response from rendering a frame of the plaintext editor.
    let resolveEnv: (v: {
      agent_id: string;
      custom_env: Record<string, string>;
    }) => void;
    mockGetAgentEnv.mockReturnValue(
      new Promise((res) => {
        resolveEnv = res;
      }),
    );
    const { rerender } = renderEnvTab(true);

    fireEvent.click(screen.getByRole("button", { name: /reveal & edit/i }));
    await waitFor(() =>
      expect(mockGetAgentEnv).toHaveBeenCalledWith("agent-1"),
    );

    // Permission is no longer known to be `true` before the response lands.
    rerender(envTabUi(null));

    // The late response must be dropped — never written to state — so the
    // plaintext editor never mounts, not even for one frame.
    await act(async () => {
      resolveEnv!({ agent_id: "agent-1", custom_env: { FOO: "bar" } });
    });

    expect(screen.queryByDisplayValue("FOO")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /save/i }),
    ).not.toBeInTheDocument();
    // `null` shows neutral pre-reveal copy, never the hard denial hint.
    expect(
      screen.queryByText(/only the agent owner or a workspace owner\/admin/i),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/1 variable configured/i)).toBeInTheDocument();
  });

  it("disables Save once permission stops being true, even with pending edits", async () => {
    mockGetAgentEnv.mockResolvedValue({
      agent_id: "agent-1",
      custom_env: { FOO: "bar" },
    });
    const { rerender } = renderEnvTab(true);

    fireEvent.click(screen.getByRole("button", { name: /reveal & edit/i }));
    const keyInput = await screen.findByDisplayValue("FOO");
    fireEvent.change(keyInput, { target: { value: "FOOX" } });
    expect(screen.getByRole("button", { name: /save/i })).toBeEnabled();

    // Permission becomes unknown (member list refetch). The editor stays
    // mounted (null doesn't trigger the cleanup effect), but Save must not be
    // clickable while we don't know the user may write.
    rerender(envTabUi(null));
    expect(screen.getByDisplayValue("FOOX")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled();
  });

  it("drops a save response that lands after canEdit stops being true mid-flight", async () => {
    mockGetAgentEnv.mockResolvedValue({
      agent_id: "agent-1",
      custom_env: { FOO: "bar" },
    });
    let resolveSave: (v: {
      agent_id: string;
      custom_env: Record<string, string>;
    }) => void;
    mockUpdateAgentEnv.mockReturnValue(
      new Promise((res) => {
        resolveSave = res;
      }),
    );
    const { rerender } = renderEnvTab(true);

    fireEvent.click(screen.getByRole("button", { name: /reveal & edit/i }));
    const keyInput = await screen.findByDisplayValue("FOO");
    fireEvent.change(keyInput, { target: { value: "FOOX" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(mockUpdateAgentEnv).toHaveBeenCalled());

    // Permission lost while the PUT is in flight.
    rerender(envTabUi(null));

    // The PUT response carries the full plaintext env; it must be dropped, so
    // the editor keeps the user's local edit instead of re-rendering secrets
    // echoed back by the server.
    await act(async () => {
      resolveSave!({ agent_id: "agent-1", custom_env: { FOO: "bar" } });
    });

    expect(screen.getByDisplayValue("FOOX")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("FOO")).not.toBeInTheDocument();
  });
});
