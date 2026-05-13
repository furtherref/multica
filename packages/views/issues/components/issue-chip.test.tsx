import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { IssueChip } from "./issue-chip";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
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
  StatusIcon: () => <span data-testid="status-icon" />,
}));

vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: any) => <>{children}</>,
  TooltipTrigger: ({ children, render }: any) => (
    <span data-testid="tooltip-trigger">{render ?? children}</span>
  ),
  TooltipContent: ({ children }: any) => <div data-testid="tooltip-content">{children}</div>,
}));

function renderChip(ui: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

describe("IssueChip", () => {
  it("renders fallback text without tooltip content when the issue is unresolved", () => {
    renderChip(<IssueChip issueId="missing-issue" fallbackLabel="MUL-404" />);

    expect(screen.getByText("MUL-404")).toBeInTheDocument();
    expect(screen.queryByTestId("tooltip-content")).not.toBeInTheDocument();
  });

  it("renders tooltip content for resolved issues", () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
        },
      },
    });
    queryClient.setQueryData(["issues", "ws-test", "list"], [
      {
        id: "issue-1",
        identifier: "MUL-1",
        title: "A very long issue title that should be available in the tooltip",
        status: "todo",
      },
    ]);

    render(
      <QueryClientProvider client={queryClient}>
        <IssueChip issueId="issue-1" />
      </QueryClientProvider>,
    );

    expect(screen.getByText("MUL-1")).toBeInTheDocument();
    expect(
      screen.getAllByText("A very long issue title that should be available in the tooltip"),
    ).toHaveLength(2);
    expect(screen.getByTestId("tooltip-content")).toHaveTextContent(
      "A very long issue title that should be available in the tooltip",
    );
  });

  it("uses the title span as the tooltip trigger content for resolved issues", () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
        },
      },
    });
    queryClient.setQueryData(["issues", "ws-test", "list"], [
      {
        id: "issue-2",
        identifier: "MUL-2",
        title: "Tooltip trigger should reuse the title span",
        status: "todo",
      },
    ]);

    render(
      <QueryClientProvider client={queryClient}>
        <IssueChip issueId="issue-2" />
      </QueryClientProvider>,
    );

    const titleInTrigger = screen.getByTestId("tooltip-trigger").querySelector(
      ".text-foreground",
    ) as HTMLElement | null;

    expect(screen.getByTestId("tooltip-trigger")).toContainElement(
      titleInTrigger,
    );
  });
});
