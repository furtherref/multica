import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IssueChip } from "./issue-chip";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@multica/core/issue-statuses/hooks", () => ({
  useIssueStatuses: () => ({
    colorOf: (status: string) =>
      status === "awaiting_response" ? "#f97316" : null,
  }),
}));

vi.mock("@multica/core/issues/queries", () => ({
  issueListOptions: () => ({
    queryKey: ["issues", "ws-test", "list"],
    queryFn: async () => [],
  }),
  issueDetailOptions: (_wsId: string, id: string) => ({
    queryKey: ["issues", "ws-test", "detail", id],
    queryFn: async () => null,
  }),
}));

vi.mock("./status-icon", () => ({
  StatusIcon: ({
    status,
    category,
    color,
    className,
  }: {
    status: string;
    category?: string;
    color?: string | null;
    className?: string;
  }) => (
    <svg
      data-testid="status-icon"
      data-status={status}
      data-category={category}
      data-color={color}
      className={className}
    />
  ),
}));

vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: any) => <>{children}</>,
  TooltipTrigger: ({ children, render }: any) => (
    <span data-testid="tooltip-trigger">{render ?? children}</span>
  ),
  TooltipContent: ({ children }: any) => <div data-testid="tooltip-content">{children}</div>,
}));

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

function renderChip(ui: ReactNode, client: QueryClient = makeClient()) {
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function seedIssue(
  client: QueryClient,
  issue: {
    id: string;
    identifier: string;
    title: string;
    status: string;
    status_category?: string;
  },
) {
  client.setQueryData(["issues", "ws-test", "list"], [issue]);
}

describe("IssueChip", () => {
  it("renders fallback text without tooltip content when the issue is unresolved", () => {
    renderChip(<IssueChip issueId="missing-issue" fallbackLabel="MUL-404" />);

    expect(screen.getByText("MUL-404")).toBeInTheDocument();
    expect(screen.queryByTestId("tooltip-content")).not.toBeInTheDocument();
  });

  it("renders tooltip content for resolved issues", () => {
    const client = makeClient();
    seedIssue(client, {
      id: "issue-1",
      identifier: "MUL-1",
      title: "A very long issue title that should be available in the tooltip",
      status: "todo",
    });

    renderChip(<IssueChip issueId="issue-1" />, client);

    expect(screen.getByText("MUL-1")).toBeInTheDocument();
    expect(
      screen.getAllByText("A very long issue title that should be available in the tooltip"),
    ).toHaveLength(2);
    expect(screen.getByTestId("tooltip-content")).toHaveTextContent(
      "A very long issue title that should be available in the tooltip",
    );
  });

  it("uses the title span as the tooltip trigger content for resolved issues", () => {
    const client = makeClient();
    seedIssue(client, {
      id: "issue-2",
      identifier: "MUL-2",
      title: "Tooltip trigger should reuse the title span",
      status: "todo",
    });

    renderChip(<IssueChip issueId="issue-2" />, client);

    const titleInTrigger = screen.getByTestId("tooltip-trigger").querySelector(
      ".text-foreground",
    ) as HTMLElement | null;

    expect(screen.getByTestId("tooltip-trigger")).toContainElement(titleInTrigger);
    expect(titleInTrigger).toHaveClass("min-w-0", "truncate");
  });

  it("caps the chip against both its content and its container, and truncates the title", () => {
    const client = makeClient();
    seedIssue(client, {
      id: "issue-3",
      identifier: "MUL-3405",
      title: "A very long issue title that should stay inside a narrow chat bubble",
      status: "todo",
    });

    renderChip(<IssueChip issueId="issue-3" />, client);

    const chip = screen.getByText("MUL-3405").closest(".issue-mention");
    // 18rem bounds the chip against a long title so it cannot dominate a line
    // of prose (#6732); 100% keeps it inside a narrow parent such as a chat
    // bubble. ProjectChip carries the identical cap — see its own test.
    expect(chip).toHaveClass("min-w-0");
    expect(chip).toHaveClass("max-w-[min(18rem,100%)]");

    const titleInTrigger = screen.getByTestId("tooltip-trigger").querySelector(
      ".text-foreground",
    ) as HTMLElement | null;
    expect(titleInTrigger).toHaveClass("min-w-0", "truncate");
  });

  it("truncates unresolved fallback labels inside the chip width", () => {
    renderChip(
      <IssueChip
        issueId="missing-issue"
        fallbackLabel="MUL-999999999999999999999999999999999"
      />,
    );

    expect(screen.getByText("MUL-999999999999999999999999999999999"))
      .toHaveClass("min-w-0", "truncate");
  });

  it("paints a custom status with its catalog color instead of the category token", () => {
    const client = makeClient();
    seedIssue(client, {
      id: "issue-4",
      identifier: "MUL-6956",
      title: "Custom status color in Chat",
      status: "awaiting_response",
      status_category: "in_review",
    });

    renderChip(<IssueChip issueId="issue-4" />, client);

    expect(screen.getByTestId("status-icon")).toHaveAttribute(
      "data-status",
      "awaiting_response",
    );
    expect(screen.getByTestId("status-icon")).toHaveAttribute(
      "data-category",
      "in_review",
    );
    expect(screen.getByTestId("status-icon")).toHaveAttribute(
      "data-color",
      "#f97316",
    );
  });
});
