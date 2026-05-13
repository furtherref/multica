import { describe, expect, it } from "vitest";
import type { InboxItem } from "../types";
import { resolveInboxEventTarget } from "./inbox-event";

function makeItem(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "ws-target",
    workspace_slug: "target",
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: null,
    actor_id: null,
    type: "mentioned",
    severity: "info",
    issue_id: "issue-1",
    title: "Mentioned you",
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2026-05-13T00:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("resolveInboxEventTarget", () => {
  it("uses the workspace carried by the inbox item instead of the current workspace", () => {
    const target = resolveInboxEventTarget(makeItem(), {
      currentWorkspaceId: "ws-current",
      currentWorkspaceSlug: "current",
    });

    expect(target).toEqual({
      workspaceId: "ws-target",
      workspaceSlug: "target",
    });
  });
});
