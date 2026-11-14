import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enAdmin from "../locales/en/admin.json";

const mockSetUserAccountStatus = vi.hoisted(() => vi.fn());
// Captures which server-side status filter the page requests; canonical
// status-param request/key behavior lives in packages/core/admin/queries.test.ts.
const mockAdminUsersOptions = vi.hoisted(() =>
  vi.fn((status: string) => ({
    queryKey: ["admin", "users", status],
    queryFn: vi.fn(),
  })),
);
const mockInvalidateQueries = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

const usersRef = vi.hoisted(() => ({
  current: [] as {
    id: string;
    name: string;
    email: string;
    avatar_url: string | null;
    account_status: "active" | "suspended" | "unknown";
    created_at: string;
  }[],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: usersRef.current }),
  useQueryClient: () => ({
    invalidateQueries: mockInvalidateQueries,
  }),
}));

vi.mock("@multica/core/admin/queries", () => ({
  adminKeys: {
    users: (status?: string) =>
      status ? ["admin", "users", status] : ["admin", "users"],
  },
  adminUsersOptions: mockAdminUsersOptions,
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("@multica/core/api", () => ({
  api: {
    setUserAccountStatus: mockSetUserAccountStatus,
  },
}));

vi.mock("@multica/core/paths", () => ({
  paths: { root: () => "/", admin: () => "/admin" },
}));

vi.mock("@multica/core/auth", () => {
  // Distinct display name from the "Alice Admin" row fixture (same id) so
  // row-level getByText queries stay unambiguous next to the sidebar footer.
  const authUser = {
    id: "user-1",
    name: "Root Operator",
    email: "root@example.com",
    avatar_url: null,
  };
  const state = { user: authUser };
  const useAuthStore = Object.assign(
    (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
    { getState: () => state },
  );
  return { useAuthStore };
});

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

import { AdminUsersPage } from "./admin-users-page";

const TEST_RESOURCES = {
  en: { common: enCommon, admin: enAdmin },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("AdminUsersPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usersRef.current = [
      // Self — the signed-in user (id: "user-1"). Active, no menu expected.
      {
        id: "user-1",
        name: "Alice Admin",
        email: "alice@example.com",
        avatar_url: null,
        account_status: "active",
        created_at: "2024-01-01T00:00:00Z",
      },
      // Suspended, not self — badge + Restore menu item.
      {
        id: "user-2",
        name: "Bob Suspended",
        email: "bob@example.com",
        avatar_url: null,
        account_status: "suspended",
        created_at: "2024-01-01T00:00:00Z",
      },
      // Active, not self — no badge + Suspend menu item.
      {
        id: "user-3",
        name: "Carol Active",
        email: "carol@example.com",
        avatar_url: null,
        account_status: "active",
        created_at: "2024-01-01T00:00:00Z",
      },
      // Unrecognized status from a newer backend — schema falls back to
      // "unknown". Must not be treated as active: neutral badge, no menu.
      {
        id: "user-4",
        name: "Dana Unknown",
        email: "dana@example.com",
        avatar_url: null,
        account_status: "unknown",
        created_at: "2024-01-01T00:00:00Z",
      },
    ];
    mockSetUserAccountStatus.mockResolvedValue({
      id: "user-3",
      name: "Carol Active",
      email: "carol@example.com",
      avatar_url: null,
      account_status: "suspended",
      created_at: "2024-01-01T00:00:00Z",
    });
  });

  it("renders rows from the query data", () => {
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    expect(screen.getByText("Alice Admin")).toBeTruthy();
    expect(screen.getByText("Bob Suspended")).toBeTruthy();
    expect(screen.getByText("Carol Active")).toBeTruthy();
  });

  it("shows a suspended badge on the suspended row only", () => {
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    const bobRow = screen.getByTestId("admin-user-row-user-2");
    const carolRow = screen.getByTestId("admin-user-row-user-3");
    expect(within(bobRow).getByText("Suspended")).toBeTruthy();
    expect(within(carolRow).queryByText("Suspended")).toBeNull();
  });

  it("shows a neutral unknown-status badge and no menu for an unrecognized account_status", () => {
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    const danaRow = screen.getByTestId("admin-user-row-user-4");
    expect(within(danaRow).getByText("Unknown status")).toBeTruthy();
    expect(within(danaRow).queryByText("Suspended")).toBeNull();
    expect(within(danaRow).queryByRole("button", { name: /more actions/i })).toBeNull();
  });

  it("does not render a menu on the current user's own row", () => {
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    const aliceRow = screen.getByTestId("admin-user-row-user-1");
    expect(within(aliceRow).getByText("You")).toBeTruthy();
    expect(within(aliceRow).queryByRole("button", { name: /more actions/i })).toBeNull();
  });

  it("fetches with status=active by default and switches the query when the filter changes", async () => {
    const user = userEvent.setup();
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    expect(mockAdminUsersOptions).toHaveBeenCalledWith("active");
    expect(mockAdminUsersOptions).not.toHaveBeenCalledWith("suspended");

    await user.click(screen.getByRole("combobox", { name: "Status" }));
    await user.click(await screen.findByRole("option", { name: "Suspended" }));

    expect(mockAdminUsersOptions).toHaveBeenCalledWith("suspended");
  });

  it("renders the sidebar: non-interactive group label, active Accounts item, and current admin identity", () => {
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    // Group label is directory-level text, not a control.
    expect(screen.getByText("System administration")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "System administration" }),
    ).toBeNull();

    // The single nav item is the current page, rendered active.
    const nav = screen.getByRole("navigation", { name: "System administration" });
    const item = within(nav).getByRole("button", { name: "Accounts" });
    expect(item.getAttribute("aria-current")).toBe("page");
    expect(item.getAttribute("data-active")).toBe("true");

    // Sidebar footer shows the signed-in admin (from the auth store).
    expect(screen.getByText("Root Operator")).toBeTruthy();
  });

  it("opens the confirm dialog for Suspend account and calls the API on confirm", async () => {
    const user = userEvent.setup();
    render(<AdminUsersPage />, { wrapper: I18nWrapper });

    const carolRow = screen.getByTestId("admin-user-row-user-3");
    await user.click(within(carolRow).getByRole("button", { name: /more actions/i }));

    const suspendItem = await screen.findByText("Suspend account");
    await user.click(suspendItem);

    await screen.findByText("Suspend Carol Active");
    const dialogConfirm = await screen.findByRole("button", { name: "Confirm" });
    await user.click(dialogConfirm);

    await waitFor(() => {
      expect(mockSetUserAccountStatus).toHaveBeenCalledWith("user-3", "suspended");
    });
    expect(mockInvalidateQueries).toHaveBeenCalledWith({
      queryKey: ["admin", "users"],
    });
  });
});
