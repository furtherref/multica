import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { JoinDiscordCard } from "./join-discord-card";

const TEST_DISCORD_URL = "https://discord.gg/furtherref-test";

// The fork keeps the Discord card but gates it on a configured DISCORD_URL.
// Drive that value from the test (a getter keeps the component reading the
// current one) so we can cover both the dormant and configured states.
const discordState = vi.hoisted(() => ({ url: "" }));
vi.mock("./discord", () => ({
  get DISCORD_URL() {
    return discordState.url;
  },
  DiscordIcon: () => null,
}));

// react-i18next isn't initialised in the views test env, so resolve the
// selector against fixed copy to assert on actual strings.
vi.mock("../i18n", () => ({
  useT: () => ({
    t: (sel: (r: { sidebar: { discord_card: Record<string, string> } }) => string) =>
      sel({
        sidebar: {
          discord_card: {
            title: "Join our Discord",
            description: "Chat with the team and other builders.",
            dismiss: "Dismiss",
          },
        },
      }),
  }),
}));

const userId = { current: "user-1" as string | undefined };
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user?: { id?: string } }) => unknown) =>
    selector({ user: userId.current ? { id: userId.current } : undefined }),
}));

beforeEach(() => {
  discordState.url = TEST_DISCORD_URL;
});

afterEach(() => {
  localStorage.clear();
  userId.current = "user-1";
});

describe("JoinDiscordCard", () => {
  it("stays dormant until a fork-owned DISCORD_URL is configured", () => {
    discordState.url = "";
    render(<JoinDiscordCard />);
    expect(screen.queryByText("Join our Discord")).not.toBeInTheDocument();
  });

  it("links to the configured Discord invite", () => {
    render(<JoinDiscordCard />);
    const link = screen.getByRole("link", { name: /join our discord/i });
    expect(link).toHaveAttribute("href", TEST_DISCORD_URL);
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("hides and stays hidden after dismiss, persisting per user", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<JoinDiscordCard />);

    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("Join our Discord")).not.toBeInTheDocument();

    // A fresh mount for the same user keeps the card hidden.
    unmount();
    render(<JoinDiscordCard />);
    expect(screen.queryByText("Join our Discord")).not.toBeInTheDocument();
  });

  it("keeps the card visible for a different user", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<JoinDiscordCard />);
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    unmount();

    userId.current = "user-2";
    render(<JoinDiscordCard />);
    expect(screen.getByText("Join our Discord")).toBeInTheDocument();
  });
});
