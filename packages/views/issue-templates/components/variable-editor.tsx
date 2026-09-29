"use client";

import { GripVertical, Plus, X } from "lucide-react";
import type { IssueTemplateVariableDefinition } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { useT } from "../../i18n";

export interface VariableDraft {
  key: string;
  label: string;
  required: boolean;
  defaultValue: string;
}

export function variablesFromConfig(config: Record<string, unknown>): VariableDraft[] {
  const raw = (config.variables ?? {}) as Record<string, IssueTemplateVariableDefinition>;
  return Object.entries(raw).map(([key, def]) => ({
    key,
    label: def.label ?? "",
    required: Boolean(def.required),
    defaultValue: def.default === undefined ? "" : String(def.default),
  }));
}

export function variablesToConfig(variables: VariableDraft[]): Record<string, IssueTemplateVariableDefinition> {
  const out: Record<string, IssueTemplateVariableDefinition> = {};
  for (const v of variables) {
    const key = v.key.trim();
    if (!key) continue;
    const def: IssueTemplateVariableDefinition = {};
    if (v.label.trim()) def.label = v.label.trim();
    if (v.required) def.required = true;
    if (v.defaultValue !== "") def.default = v.defaultValue;
    out[key] = def;
  }
  return out;
}

/**
 * Editor for a template's declared {{variables}}. A variable is referenced in
 * the issue title/content as {{name}} and instantiated with a value at
 * create time; the definitions here (label / required / default) feed the
 * instantiate endpoint.
 */
export function VariableEditor({
  value,
  onChange,
}: {
  value: VariableDraft[];
  onChange: (next: VariableDraft[]) => void;
}) {
  const { t } = useT("issue-templates");

  const setRow = (index: number, patch: Partial<VariableDraft>) => {
    onChange(value.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  };

  return (
    <div className="space-y-2">
      <Label className="text-caption text-muted-foreground">
        {t(($) => $.variables.title)}
      </Label>
      {value.length === 0 && (
        <p className="text-caption text-muted-foreground">
          {t(($) => $.variables.empty)}
        </p>
      )}
      <div className="space-y-2">
        {value.map((row, index) => (
          <div key={index} className="flex items-start gap-2 rounded-md border border-surface-border px-2 py-2">
            <GripVertical className="mt-1 size-4 shrink-0 text-faint-foreground" />
            <div className="grid min-w-0 flex-1 grid-cols-2 gap-2 sm:grid-cols-[minmax(6rem,1fr)_minmax(6rem,1fr)_minmax(6rem,1fr)_auto]">
              <div className="space-y-1">
                <Label htmlFor={`var-key-${index}`} className="text-micro text-muted-foreground">
                  {t(($) => $.variables.key)}
                </Label>
                <Input
                  id={`var-key-${index}`}
                  value={row.key}
                  onChange={(e) => setRow(index, { key: e.target.value })}
                  placeholder={t(($) => $.variables.key_placeholder)}
                  className="h-8 font-mono text-caption"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`var-label-${index}`} className="text-micro text-muted-foreground">
                  {t(($) => $.variables.label)}
                </Label>
                <Input
                  id={`var-label-${index}`}
                  value={row.label}
                  onChange={(e) => setRow(index, { label: e.target.value })}
                  placeholder={t(($) => $.variables.label_placeholder)}
                  className="h-8 text-caption"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`var-default-${index}`} className="text-micro text-muted-foreground">
                  {t(($) => $.variables.default)}
                </Label>
                <Input
                  id={`var-default-${index}`}
                  value={row.defaultValue}
                  onChange={(e) => setRow(index, { defaultValue: e.target.value })}
                  placeholder={t(($) => $.variables.default_placeholder)}
                  className="h-8 text-caption"
                />
              </div>
              <div className="flex items-end gap-1">
                <label className="flex cursor-pointer items-center gap-1.5 pb-2 text-caption text-muted-foreground">
                  <Checkbox
                    checked={row.required}
                    onCheckedChange={(checked) => setRow(index, { required: Boolean(checked) })}
                  />
                  {t(($) => $.variables.required)}
                </label>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t(($) => $.variables.remove, { name: row.key || `#${index + 1}` })}
                  disabled={value.length <= 1}
                  onClick={() => onChange(value.filter((_, i) => i !== index))}
                  className="ml-auto"
                >
                  <X className="size-3.5" />
                </Button>
              </div>
            </div>
          </div>
        ))}
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="gap-2"
        onClick={() =>
          onChange([...value, { key: "", label: "", required: false, defaultValue: "" }])
        }
      >
        <Plus className="size-3.5" />
        {t(($) => $.variables.add)}
      </Button>
    </div>
  );
}