import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useWSClient } from "./realtime-provider";
import { bindTaskMessageScopes } from "./task-message-scopes";

/** Holds private task scopes for exactly the lifetime of observed transcript queries. */
export function useTaskMessageScopes() {
  const queryClient = useQueryClient();
  const ws = useWSClient();

  useEffect(() => {
    if (!ws) return;
    return bindTaskMessageScopes(queryClient, ws);
  }, [queryClient, ws]);
}
