"use client";

import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { Loader2 } from "lucide-react";
import { useT } from "../i18n";
import { UnsupportedFallback } from "./attachment-preview-fallback";
import { loadDocsApi, type DocsApiEditor } from "./utils/docs-api-loader";

export function OfficeAttachmentPreview({
  attachmentId,
  onDownload,
}: {
  attachmentId: string;
  onDownload: () => void;
}) {
  const { t } = useT("editor");
  // DocEditor needs a DOM-id-safe placeholder; useId() yields ":r0:" — strip
  // the colons so the id is also a valid CSS selector.
  const placeholderId = `office-${useId().replace(/:/g, "_")}`;
  const editorRef = useRef<DocsApiEditor | null>(null);
  const [loadError, setLoadError] = useState(false);

  const { data, isPending, isError } = useQuery({
    queryKey: ["office-config", attachmentId],
    queryFn: () => api.getOfficeConfig(attachmentId),
    enabled: !!attachmentId,
    retry: false,
    staleTime: 60_000,
  });

  useEffect(() => {
    if (!data?.document_server_url) return;
    let cancelled = false;
    setLoadError(false);
    loadDocsApi(data.document_server_url)
      .then((docs) => {
        if (cancelled) return;
        editorRef.current = new docs.DocEditor(placeholderId, data.config);
      })
      .catch(() => {
        // api.js failed to load (DS unreachable / CSP / network). Surface the
        // download fallback instead of leaving a blank placeholder.
        if (!cancelled) setLoadError(true);
      });
    return () => {
      cancelled = true;
      try {
        editorRef.current?.destroyEditor();
      } catch {
        // Editor may not have finished initializing; destroy is best-effort.
      }
      editorRef.current = null;
    };
  }, [data, placeholderId]);

  if (isPending) {
    return (
      <div className="flex h-full w-full items-center justify-center">
        <Loader2 className="size-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (isError || !data?.document_server_url || loadError) {
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_unsupported)}
        onDownload={onDownload}
      />
    );
  }
  return <div id={placeholderId} className="h-full w-full" />;
}
