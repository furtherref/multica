"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, ScrollText } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { api } from "@multica/core/api";
import {
  chatKeys,
  isTaskMessageTaskId,
  mergeTaskMessagesBySeq,
  taskMessagesOptions,
} from "@multica/core/chat/queries";
import type { AgentTask } from "@multica/core/types/agent";
import type { TaskMessagePayload } from "@multica/core/types/events";
import { AgentTranscriptDialog } from "./agent-transcript-dialog";
import { buildTimeline, type TimelineItem } from "./build-timeline";

type CatchupStatus = "idle" | "pending" | "verified" | "failed";

interface TranscriptButtonProps {
  task: AgentTask;
  agentName: string;
  /**
   * Pre-loaded timeline. When provided the button skips the shared cache and
   * renders these items directly — used by surfaces that already own a live
   * timeline (e.g. a card whose `items` accumulate via WS). Omit it and the
   * button reads the shared `task-messages` cache instead.
   */
  items?: TimelineItem[];
  isLive?: boolean;
  /** Transient activity hint (e.g. "reconnecting") forwarded to the dialog's
   *  empty-state live label. Supply from the same source as `items`. */
  activity?: string;
  className?: string;
  title?: string;
  renderButton?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /**
   * Optional content rendered above the transcript event list. Used to
   * surface autopilot webhook payloads inline with the run history.
   */
  headerSlot?: React.ReactNode;
}

/**
 * Compact icon-button that opens the full transcript dialog. Used on any
 * surface that lists agent tasks (issue execution log, issue header chip,
 * agent detail activity tab). Owns its own dialog state; the parent just
 * drops it in.
 */
export function TranscriptButton({
  task,
  agentName,
  items: providedItems,
  isLive = false,
  activity,
  className,
  title = "View transcript",
  renderButton = true,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  headerSlot,
}: TranscriptButtonProps) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const open = controlledOpen ?? uncontrolledOpen;
  const setOpen = controlledOnOpenChange ?? setUncontrolledOpen;

  // Two modes share one transcript surface:
  //   - A live card may hand us `items` directly (already accumulating via WS
  //     in the parent) — we render them as-is.
  //   - Every other surface (issue execution log, header chip, agent activity
  //     tab) omits `items`; we read the shared `task-messages` cache — the same
  //     entry `useRealtimeSync` seeds from `task:message` events. This keeps all
  //     entry points on one source of truth and lets an open dialog grow live,
  //     instead of each button freezing its own one-shot fetch (which surfaced
  //     empty/partial logs when opened early in a run).
  const usesSharedCache = providedItems === undefined;
  const canFetch = usesSharedCache && isTaskMessageTaskId(task.id);
  const qc = useQueryClient();

  // Subscribe to the shared `task-messages` cache for reactive reads (the same
  // entry `useRealtimeSync` seeds from `task:message` events), but DON'T let the
  // query drive fetching — a query refetch replaces the whole cache, which would
  // clobber a WS increment that arrived while the HTTP read was in flight. We
  // fetch manually below and merge by seq instead.
  const { data: messages } = useQuery({
    ...taskMessagesOptions(task.id),
    enabled: false,
  });

  const [catchupStatus, setCatchupStatus] =
    useState<CatchupStatus>("idle");
  const catchupSeq = useRef(0);

  // Catch up from the server each time the dialog opens. `task:message`
  // increments can be lost across a WS disconnect, and the cache is never
  // invalidated on reconnect/completion (staleTime: Infinity), so the warm
  // cache alone could show a permanently partial transcript. Reconcile with the
  // authoritative DB list by MERGING into live cache (not replacing) so a
  // concurrent WS append is never dropped; WS keeps it live while open.
  const runCatchup = useCallback(async () => {
    if (!canFetch) return;
    const seq = ++catchupSeq.current;
    setCatchupStatus("pending");
    try {
      const fetched = await api.listTaskMessages(task.id);
      qc.setQueryData<TaskMessagePayload[]>(
        chatKeys.taskMessages(task.id),
        (current) => mergeTaskMessagesBySeq(current ?? [], fetched),
      );
      if (catchupSeq.current === seq) setCatchupStatus("verified");
    } catch {
      if (catchupSeq.current !== seq) return;
      setCatchupStatus("failed");
    }
  }, [canFetch, task.id, qc]);

  useEffect(() => {
    catchupSeq.current += 1;
    setCatchupStatus("idle");
  }, [task.id]);

  // Catch up on open, and again the moment the task reaches a terminal
  // state — the shared cache can still be missing the final tail of
  // messages a completed task never re-broadcasts over WS.
  useEffect(() => {
    if (open && canFetch) void runCatchup();
  }, [open, canFetch, runCatchup, isLive]);

  const rawItems = useMemo(
    () => providedItems ?? buildTimeline(messages ?? []),
    [providedItems, messages],
  );

  // A terminal task only treats the transcript as complete after the
  // authoritative /messages catch-up succeeds. A warm cache can still be useful
  // to display partial content, but it is not proof of completeness because WS
  // events can be missed during reconnects.
  const needsVerifiedCatchup = open && canFetch && !isLive;
  const catchupPending =
    needsVerifiedCatchup &&
    catchupStatus !== "verified" &&
    catchupStatus !== "failed";
  const catchupIncomplete =
    needsVerifiedCatchup && catchupStatus !== "verified";
  const awaitingFirstLoad =
    catchupPending && messages === undefined;
  const dialogReady = open && !awaitingFirstLoad;
  const items = rawItems;

  const handleClick = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (canFetch) setCatchupStatus("pending");
    setOpen(true);
  }, [canFetch]);

  useEffect(() => {
    if (!open) return;

    const handleGlobalNavigate = () => {
      setOpen(false);
    };

    window.addEventListener("multica:navigate", handleGlobalNavigate);
    return () => {
      window.removeEventListener("multica:navigate", handleGlobalNavigate);
    };
  }, [open, setOpen]);

  return (
    <>
      {renderButton ? (
        <Tooltip>
          <TooltipTrigger
            render={<button type="button" />}
            onClick={handleClick}
            disabled={awaitingFirstLoad}
            aria-label={title}
            className={cn(
              "flex items-center justify-center rounded p-1 text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors disabled:opacity-50",
              className,
            )}
          >
            {awaitingFirstLoad ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <ScrollText className="h-3.5 w-3.5" />
            )}
          </TooltipTrigger>
          <TooltipContent>{title}</TooltipContent>
        </Tooltip>
      ) : null}

      {dialogReady && (
        <AgentTranscriptDialog
          open={open}
          onOpenChange={setOpen}
          task={task}
          items={items}
          agentName={agentName}
          isLive={isLive}
          activity={activity}
          headerSlot={headerSlot}
          loadIncomplete={catchupIncomplete}
          loadPending={catchupPending}
          onRetryLoad={runCatchup}
          retrying={catchupPending}
        />
      )}
    </>
  );
}
