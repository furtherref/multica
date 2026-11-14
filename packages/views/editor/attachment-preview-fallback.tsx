"use client";

import { Download, FileText } from "lucide-react";
import { useT } from "../i18n";

// Used for 413 / 415 / unknown kinds and a failed office load. Sits on the
// viewer's near-black stage, so it wears the dark token set.
export function UnsupportedFallback({
  message,
  onDownload,
}: {
  message: string;
  onDownload: () => void;
}) {
  const { t } = useT("editor");
  return (
    <div className="dark flex h-full flex-col items-center justify-center gap-3 px-8 text-center">
      <FileText className="size-8 text-muted-foreground" />
      <p className="text-body text-muted-foreground">{message}</p>
      <button
        type="button"
        className="inline-flex items-center gap-2 rounded-md border border-input bg-secondary px-3 py-1.5 text-body text-foreground transition-colors hover:bg-muted"
        onClick={onDownload}
      >
        <Download className="size-4" />
        {t(($) => $.image.download)}
      </button>
    </div>
  );
}
