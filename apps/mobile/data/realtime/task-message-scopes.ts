import type { QueryClient } from "@tanstack/react-query";
import type { TaskMessagePayload } from "@multica/core/types";
import { isTaskMessageTaskId } from "@/data/queries/task-message-id";
import type { WSClient } from "./ws-client";

interface ObservableQuery {
  queryKey: readonly unknown[];
  getObserversCount: () => number;
}

/**
 * Mirrors the web/core task transcript scope contract using the mobile-owned
 * QueryClient. QueryCache emits observerAdded synchronously before the first
 * fetch starts, so the server subscription is established before history is
 * requested and stays alive until the final surface releases the query.
 */
export function bindTaskMessageScopes(queryClient: QueryClient, ws: WSClient) {
  const releases = new Map<string, () => void>();

  const syncQuery = (query: ObservableQuery) => {
    const [prefix, taskId, ...rest] = query.queryKey;
    if (
      prefix !== "task-messages" ||
      rest.length > 0 ||
      typeof taskId !== "string" ||
      !isTaskMessageTaskId(taskId)
    ) {
      return;
    }

    const active = query.getObserversCount() > 0;
    const release = releases.get(taskId);
    if (active && !release) {
      releases.set(taskId, ws.subscribeScope("task", taskId));
    } else if (!active && release) {
      release();
      releases.delete(taskId);
    }
  };

  const cache = queryClient.getQueryCache();
  for (const query of cache.getAll()) syncQuery(query);
  const unsubscribe = cache.subscribe((event) => syncQuery(event.query));
  const unsubscribeMessages = ws.on("task:message", (payload) => {
    if (!releases.has(payload.task_id)) return;
    queryClient.setQueryData<TaskMessagePayload[]>(
      ["task-messages", payload.task_id],
      (old = []) => {
        if (old.some((message) => message.seq === payload.seq)) return old;
        return [...old, payload].sort((left, right) => left.seq - right.seq);
      },
    );
  });

  return () => {
    unsubscribe();
    unsubscribeMessages();
    for (const release of releases.values()) release();
    releases.clear();
  };
}
