"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowLeft,
  ChevronRight,
  Eye,
  FileText,
  Loader2,
  Pencil,
  Save,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { useTimeAgo } from "../../i18n";
import type { IssueTemplate, MemberWithUser, UpdateIssueTemplateRequest } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueTemplateDetailOptions, useDeleteIssueTemplate, useUpdateIssueTemplate } from "@multica/core/issue-templates";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { ReadonlyContent } from "../../editor";
import { AppLink, useNavigation } from "../../navigation";
import { useT } from "../../i18n";

function seedDraft(template: IssueTemplate) {
  return {
    name: template.name,
    issueTitle: template.issue_title,
    issueContent: template.issue_content,
  };
}

export function IssueTemplateDetailPage({ templateId }: { templateId: string }) {
  const { t } = useT("issue-templates");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const updateTemplate = useUpdateIssueTemplate();
  const deleteTemplate = useDeleteIssueTemplate();

  const {
    data: template,
    isLoading,
    error,
  } = useQuery(issueTemplateDetailOptions(wsId, templateId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  const [name, setName] = useState("");
  const [issueTitle, setIssueTitle] = useState("");
  const [issueContent, setIssueContent] = useState("");
  const [editingContent, setEditingContent] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const seededIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (!template) return;
    if (seededIdRef.current === template.id) return;
    const draft = seedDraft(template);
    setName(draft.name);
    setIssueTitle(draft.issueTitle);
    setIssueContent(draft.issueContent);
    seededIdRef.current = template.id;
  }, [template]);

  const creator = useMemo<MemberWithUser | null>(
    () =>
      template?.created_by
        ? members.find((m) => m.user_id === template.created_by) ?? null
        : null,
    [members, template?.created_by],
  );

  const isDirty = useMemo(() => {
    if (!template) return false;
    return (
      name.trim() !== template.name ||
      issueTitle.trim() !== template.issue_title ||
      issueContent !== template.issue_content
    );
  }, [template, name, issueTitle, issueContent]);

  const handleSave = async () => {
    if (!template) return;
    const payload: UpdateIssueTemplateRequest = {};
    if (name.trim() !== template.name) payload.name = name.trim();
    if (issueTitle.trim() !== template.issue_title) payload.issue_title = issueTitle.trim();
    if (issueContent !== template.issue_content) payload.issue_content = issueContent;
    try {
      const updated = await updateTemplate.mutateAsync({
        id: template.id,
        ...payload,
      });
      seededIdRef.current = updated.id;
      toast.success(t(($) => $.detail.toast_saved));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t(($) => $.detail.toast_save_failed));
    }
  };

  const handleDiscard = () => {
    if (!template) return;
    const draft = seedDraft(template);
    setName(draft.name);
    setIssueTitle(draft.issueTitle);
    setIssueContent(draft.issueContent);
  };

  const handleDelete = async () => {
    if (!template) return;
    try {
      await deleteTemplate.mutateAsync(template.id);
      navigation.replace(paths.issueTemplates());
      toast.success(t(($) => $.detail.toast_deleted));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t(($) => $.detail.toast_delete_failed));
      setConfirmDelete(false);
    }
  };

  if (isLoading) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <Skeleton className="h-7 w-24" />
          <Skeleton className="h-4 w-4" />
          <Skeleton className="h-4 w-40" />
        </div>
        <div className="space-y-4 p-6">
          <Skeleton className="h-9 w-full max-w-xl" />
          <Skeleton className="h-9 w-full max-w-2xl" />
          <Skeleton className="h-64 w-full max-w-3xl" />
        </div>
      </div>
    );
  }

  if (error || !template || !template.id) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
          <Button
            variant="ghost"
            size="xs"
            render={<AppLink href={paths.issueTemplates()} />}
          >
            <ArrowLeft className="h-3 w-3" />
            {t(($) => $.detail.all_templates)}
          </Button>
        </div>
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-center">
          <AlertCircle className="h-8 w-8 text-faint-foreground" />
          <p className="text-body font-medium">{t(($) => $.detail.not_found.title)}</p>
          <p className="max-w-xs text-caption text-muted-foreground">
            {error instanceof Error ? error.message : t(($) => $.detail.not_found.fallback)}
          </p>
          <AppLink
            href={paths.issueTemplates()}
            className={`${buttonVariants({ variant: "outline", size: "xs" })} mt-2`}
          >
            {t(($) => $.detail.not_found.back)}
          </AppLink>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button
          variant="ghost"
          size="xs"
          render={<AppLink href={paths.issueTemplates()} />}
        >
          <ArrowLeft className="h-3 w-3" />
          {t(($) => $.detail.all_templates)}
        </Button>
        <ChevronRight className="h-3 w-3 text-muted-foreground" />
        <span className="truncate font-mono text-caption text-foreground">
          {template.name}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => setConfirmDelete(true)}
          className="ml-auto text-muted-foreground hover:text-destructive"
          aria-label={t(($) => $.detail.delete_aria)}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="flex min-h-0 flex-1">
        <section className="flex min-w-0 flex-1 flex-col">
          <div className="space-y-4 overflow-y-auto px-6 py-5">
            <div className="space-y-1.5">
              <Label htmlFor="issue-template-detail-name" className="text-caption text-muted-foreground">
                {t(($) => $.fields.name)}
              </Label>
              <Input
                id="issue-template-detail-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="h-9 max-w-xl text-title font-semibold"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="issue-template-detail-title" className="text-caption text-muted-foreground">
                <FileText className="h-3 w-3" />
                {t(($) => $.fields.issue_title)}
              </Label>
              <Input
                id="issue-template-detail-title"
                value={issueTitle}
                onChange={(e) => setIssueTitle(e.target.value)}
                className="max-w-2xl"
              />
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label className="text-caption text-muted-foreground">
                  {t(($) => $.fields.issue_content)}
                </Label>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => setEditingContent(!editingContent)}
                        className="text-muted-foreground"
                      >
                        {editingContent ? (
                          <Eye className="h-3.5 w-3.5" />
                        ) : (
                          <Pencil className="h-3.5 w-3.5" />
                        )}
                      </Button>
                    }
                  />
                  <TooltipContent>
                    {editingContent
                      ? t(($) => $.detail.content_preview)
                      : t(($) => $.detail.content_edit)}
                  </TooltipContent>
                </Tooltip>
              </div>
              {editingContent ? (
                <Textarea
                  id="issue-template-detail-content"
                  value={issueContent}
                  onChange={(e) => setIssueContent(e.target.value)}
                  rows={18}
                  className="min-h-72 resize-y font-mono text-caption"
                />
              ) : (
                <div className="rounded-md border px-4 py-3 min-h-72">
                  {issueContent ? (
                    <ReadonlyContent content={issueContent} />
                  ) : (
                    <p className="text-body text-muted-foreground italic">
                      {t(($) => $.detail.no_content)}
                    </p>
                  )}
                </div>
              )}
            </div>
          </div>

          {isDirty && (
            <div
              role="status"
              aria-live="polite"
              className="flex items-center gap-2 border-t bg-muted/30 px-4 py-2"
            >
              <span className="h-1.5 w-1.5 rounded-full bg-brand" />
              <span className="text-caption text-muted-foreground">
                {t(($) => $.detail.save_bar.unsaved)}
              </span>
              <div className="ml-auto flex items-center gap-1.5">
                <Button type="button" variant="ghost" size="xs" onClick={handleDiscard}>
                  {t(($) => $.detail.save_bar.discard)}
                </Button>
                <Button
                  type="button"
                  size="xs"
                  onClick={handleSave}
                  disabled={
                    updateTemplate.isPending || !name.trim() || !issueTitle.trim()
                  }
                >
                  {updateTemplate.isPending ? (
                    <>
                      <Loader2 className="h-3 w-3 animate-spin" />
                      {t(($) => $.detail.save_bar.saving)}
                    </>
                  ) : (
                    <>
                      <Save className="h-3 w-3" />
                      {t(($) => $.detail.save_bar.save)}
                    </>
                  )}
                </Button>
              </div>
            </div>
          )}
        </section>

        <aside className="flex w-72 shrink-0 flex-col gap-4 overflow-y-auto border-l bg-muted/20 px-4 py-4">
          <div>
            <h3 className="mb-2 text-caption font-medium uppercase tracking-wider text-muted-foreground">
              {t(($) => $.detail.sidebar.metadata)}
            </h3>
            <dl className="space-y-1.5 text-caption">
              <div className="flex gap-2">
                <dt className="min-w-20 text-muted-foreground">
                  {t(($) => $.detail.sidebar.source)}
                </dt>
                <dd className="min-w-0 flex-1">{t(($) => $.table.source_manual)}</dd>
              </div>
              <div className="flex gap-2">
                <dt className="min-w-20 text-muted-foreground">
                  {t(($) => $.detail.sidebar.created)}
                </dt>
                <dd className="min-w-0 flex-1">{timeAgo(template.created_at)}</dd>
              </div>
              <div className="flex gap-2">
                <dt className="min-w-20 text-muted-foreground">
                  {t(($) => $.detail.sidebar.updated)}
                </dt>
                <dd className="min-w-0 flex-1">{timeAgo(template.updated_at)}</dd>
              </div>
              {creator && (
                <div className="flex gap-2">
                  <dt className="min-w-20 text-muted-foreground">
                    {t(($) => $.detail.sidebar.created_by)}
                  </dt>
                  <dd className="min-w-0 flex-1">
                    <span className="inline-flex min-w-0 items-center gap-1.5">
                      <ActorAvatar
                        name={creator.name}
                        initials={creator.name.slice(0, 2).toUpperCase()}
                        avatarUrl={creator.avatar_url}
                        size="xs"
                      />
                      <span className="truncate">{creator.name}</span>
                    </span>
                  </dd>
                </div>
              )}
              <div className="flex gap-2" title={template.id}>
                <dt className="min-w-20 text-muted-foreground">
                  {t(($) => $.detail.sidebar.id)}
                </dt>
                <dd className="min-w-0 flex-1 truncate font-mono text-muted-foreground">
                  {template.id.slice(0, 8)}...
                </dd>
              </div>
            </dl>
          </div>
        </aside>
      </div>

      <Dialog
        open={confirmDelete}
        onOpenChange={(v) => {
          if (!v) setConfirmDelete(false);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.detail.delete_dialog.title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.detail.delete_dialog.description, { name: template.name })}
            </DialogDescription>
          </DialogHeader>
          <div className="rounded-md bg-destructive/10 px-3 py-2 text-caption text-destructive">
            {t(($) => $.detail.delete_dialog.warning)}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setConfirmDelete(false)}
              disabled={deleteTemplate.isPending}
            >
              {t(($) => $.detail.delete_dialog.cancel)}
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteTemplate.isPending}
            >
              {deleteTemplate.isPending ? (
                <>
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t(($) => $.detail.delete_dialog.deleting)}
                </>
              ) : (
                <>
                  <Trash2 className="h-3 w-3" />
                  {t(($) => $.detail.delete_dialog.confirm)}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
