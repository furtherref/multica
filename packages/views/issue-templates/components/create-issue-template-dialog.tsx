"use client";

import { useState } from "react";
import { AlertCircle, FileText, Loader2, Plus, X } from "lucide-react";
import { toast } from "sonner";
import type { IssueTemplate } from "@multica/core/types";
import { useCreateIssueTemplate } from "@multica/core/issue-templates";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";

export function CreateIssueTemplateDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (template: IssueTemplate) => void;
}) {
  const { t } = useT("issue-templates");
  const createTemplate = useCreateIssueTemplate();
  const [name, setName] = useState("");
  const [issueTitle, setIssueTitle] = useState("");
  const [issueContent, setIssueContent] = useState("");
  const [error, setError] = useState("");

  const submit = async () => {
    const trimmedName = name.trim();
    const trimmedTitle = issueTitle.trim();
    if (!trimmedName || !trimmedTitle) return;

    setError("");
    try {
      const template = await createTemplate.mutateAsync({
        name: trimmedName,
        issue_title: trimmedTitle,
        issue_content: issueContent,
      });
      toast.success(t(($) => $.create.toast_created));
      onCreated(template);
    } catch (err) {
      setError(err instanceof Error ? err.message : t(($) => $.create.fallback_error));
    }
  };

  const loading = createTemplate.isPending;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[82vh] flex-col overflow-hidden p-0 sm:max-w-2xl">
        <div className="flex h-12 shrink-0 items-center justify-between border-b px-5">
          <div>
            <DialogTitle className="text-body font-semibold">
              {t(($) => $.create.title)}
            </DialogTitle>
            <p className="mt-0.5 text-caption text-muted-foreground">
              {t(($) => $.create.description)}
            </p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            disabled={loading}
            aria-label={t(($) => $.create.close)}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
          <div className="space-y-1.5">
            <Label htmlFor="issue-template-name" className="text-caption text-muted-foreground">
              {t(($) => $.fields.name)}
            </Label>
            <Input
              id="issue-template-name"
              autoFocus
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setError("");
              }}
              placeholder={t(($) => $.create.name_placeholder)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="issue-template-title" className="text-caption text-muted-foreground">
              <FileText className="h-3 w-3" />
              {t(($) => $.fields.issue_title)}
            </Label>
            <Input
              id="issue-template-title"
              value={issueTitle}
              onChange={(e) => {
                setIssueTitle(e.target.value);
                setError("");
              }}
              placeholder={t(($) => $.create.issue_title_placeholder)}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="issue-template-content" className="text-caption text-muted-foreground">
              {t(($) => $.fields.issue_content)}
            </Label>
            <Textarea
              id="issue-template-content"
              value={issueContent}
              onChange={(e) => setIssueContent(e.target.value)}
              placeholder={t(($) => $.create.issue_content_placeholder)}
              rows={10}
              className="resize-none font-mono text-caption"
            />
          </div>

          {error && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-caption text-destructive"
            >
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{error}</span>
            </div>
          )}
        </div>

        <div className="flex shrink-0 items-center justify-end gap-2 border-t bg-muted/30 px-5 py-3">
          <Button type="button" variant="ghost" size="sm" onClick={onClose} disabled={loading}>
            {t(($) => $.create.cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={submit}
            disabled={!name.trim() || !issueTitle.trim() || loading}
          >
            {loading ? (
              <>
                <Loader2 className="h-3 w-3 animate-spin" />
                {t(($) => $.create.submitting)}
              </>
            ) : (
              <>
                <Plus className="h-3 w-3" />
                {t(($) => $.create.submit)}
              </>
            )}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
