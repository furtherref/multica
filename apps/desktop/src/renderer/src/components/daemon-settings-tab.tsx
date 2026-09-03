import { useState, useEffect, useCallback, type ReactNode } from "react";
import { AlertCircle, Info, LogIn } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
  SettingsTab,
} from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { reauthenticateDaemon } from "../platform/daemon-reauth";
import type { DaemonPrefs, DaemonStatus } from "../../../shared/daemon-types";
import {
  DAEMON_STATE_COLORS,
  formatUptime,
} from "../../../shared/daemon-types";
import { daemonStateLabel } from "./daemon-i18n";

// One row inside the diagnostics block. Values that are likely to be
// long IDs / URLs render as monospaced + truncated with a tooltip.
function DiagnosticsRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="grid grid-cols-[140px_minmax(0,1fr)] items-baseline gap-3 py-1.5">
      <span className="text-caption text-muted-foreground">{label}</span>
      <span
        className={cn(
          "min-w-0 truncate text-body",
          mono && "font-mono text-caption",
        )}
        title={typeof value === "string" ? value : undefined}
      >
        {value}
      </span>
    </div>
  );
}

export function DaemonSettingsTab() {
  const { t } = useT("settings");
  const [prefs, setPrefs] = useState<DaemonPrefs>({ autoStart: true, autoStop: false });
  const [cliInstalled, setCliInstalled] = useState<boolean | null>(null);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });
  const [confirmNewRoot, setConfirmNewRoot] = useState<string | null>(null);
  const [reauthLoading, setReauthLoading] = useState(false);

  useEffect(() => {
    window.daemonAPI.getPrefs().then(setPrefs);
    window.daemonAPI.isCliInstalled().then(setCliInstalled);
    window.daemonAPI.getStatus().then(setStatus);
    return window.daemonAPI.onStatusChange(setStatus);
  }, []);

  const handleReauth = useCallback(async () => {
    setReauthLoading(true);
    await reauthenticateDaemon(t);
    setReauthLoading(false);
  }, [t]);

  const updatePref = useCallback(
    async (key: keyof DaemonPrefs, value: boolean) => {
      setSaving(true);
      try {
        const updated = await window.daemonAPI.setPrefs({ [key]: value });
        setPrefs(updated);
        toast.success(t(($) => $.desktop.daemon.settings_saved), {
          id: "settings-auto-save",
        });
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t(($) => $.desktop.daemon.settings_save_failed),
        );
      } finally {
        setSaving(false);
      }
    },
    [t],
  );

  const handlePickDirectory = useCallback(async () => {
    const result = await window.daemonAPI.pickDirectory();
    if (result.canceled || !result.path) return;
    setConfirmNewRoot(result.path);
  }, []);

  const handleConfirmRootChange = useCallback(async () => {
    if (!confirmNewRoot) return;
    setSaving(true);
    const updated = await window.daemonAPI.setPrefs({ workspacesRoot: confirmNewRoot });
    setPrefs(updated);
    setConfirmNewRoot(null);
    setSaving(false);
    // Restart the daemon so the new workspaces_root takes effect
    if (status.state === "running") {
      await window.daemonAPI.restart();
    }
  }, [confirmNewRoot, status.state]);

  // The effective workspaces root: from daemon status if running, else from prefs
  const effectiveRoot = status.workspacesRoot ?? prefs.workspacesRoot;

  // The daemon runs somewhere the app can't drive (e.g. inside WSL2 behind a
  // Windows desktop): /health is reachable but the lifecycle CLI can't reach
  // its process. Auto-start/auto-stop can't work, so disable them and say why
  // rather than letting the toggles silently no-op. See #3916.
  const externallyManaged = status.externallyManaged === true;

  return (
    <SettingsTab
      title={t(($) => $.desktop.daemon.title)}
      description={t(($) => $.desktop.daemon.description)}
    >

      {status.state === "auth_expired" && (
        <div className="mt-4 flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div className="min-w-0 flex-1">
            <p className="text-body font-medium text-destructive">
              {t(($) => $.desktop.daemon.signin_expired)}
            </p>
            <p className="mt-0.5 text-body text-muted-foreground">
              {t(($) => $.desktop.daemon.signin_expired_description)}
            </p>
          </div>
          <Button
            size="sm"
            className="shrink-0"
            onClick={handleReauth}
            disabled={reauthLoading}
          >
            <LogIn className="size-3.5 mr-1.5" />
            {t(($) => $.desktop.daemon.signin_again)}
          </Button>
        </div>
      )}

      {externallyManaged && (
        <div className="mt-4 flex items-start gap-3 rounded-lg border bg-muted/30 px-4 py-3">
          <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <p className="min-w-0 text-body text-muted-foreground">
            {t(($) => $.desktop.daemon.external_description_before)}{" "}
            <code className="font-mono text-caption">multica daemon start</code> /{" "}
            <code className="font-mono text-caption">multica daemon stop</code>
            {t(($) => $.desktop.daemon.external_description_after)}
          </p>
        </div>
      )}

      <SettingsCard>
        <SettingsRow
          label={t(($) => $.desktop.daemon.auto_start_title)}
          description={t(($) => $.desktop.daemon.auto_start_description)}
        >
          <Switch
            checked={prefs.autoStart}
            onCheckedChange={(checked) => updatePref("autoStart", checked)}
            disabled={saving || externallyManaged}
          />
        </SettingsRow>

        <SettingsRow
          label={t(($) => $.desktop.daemon.auto_stop_title)}
          description={t(($) => $.desktop.daemon.auto_stop_description)}
        >
          <Switch
            checked={prefs.autoStop}
            onCheckedChange={(checked) => updatePref("autoStop", checked)}
            disabled={saving || externallyManaged}
          />
        </SettingsRow>

        <SettingsRow
          label="Repos Storage Location"
          description={
            effectiveRoot
              ? `Directory where workspace repositories and task environments are stored: ${effectiveRoot}`
              : "Directory where workspace repositories and task environments are stored."
          }
        >
          <Button
            variant="outline"
            size="sm"
            onClick={handlePickDirectory}
            disabled={saving}
          >
            Change
          </Button>
        </SettingsRow>

        <SettingsRow
          label={t(($) => $.desktop.daemon.cli_status)}
          description={
            cliInstalled === null
              ? t(($) => $.desktop.daemon.cli_checking)
              : cliInstalled
                ? t(($) => $.desktop.daemon.cli_installed)
                : t(($) => $.desktop.daemon.cli_missing)
          }
        >
          {cliInstalled === false && (
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                window.desktopAPI.openExternal(
                  "https://github.com/furtherref/multica#cli-installation",
                )
              }
            >
              {t(($) => $.desktop.daemon.installation_guide)}
            </Button>
          )}
          {cliInstalled !== false && <span />}
        </SettingsRow>
      </SettingsCard>

      <AlertDialog open={confirmNewRoot !== null} onOpenChange={(open) => !open && setConfirmNewRoot(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Change repos storage location?</AlertDialogTitle>
            <AlertDialogDescription>
              The daemon will store repos and task environments in:{' '}
              <span className="font-mono text-caption bg-muted/50 px-1.5 py-0.5 rounded break-all">
                {confirmNewRoot}
              </span>
              . The daemon will be restarted to apply this change. Existing repos at the current
              location will not be moved automatically — copy them manually if needed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleConfirmRootChange} disabled={saving}>
              Change &amp; Restart
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Diagnostics — moved out of the logs panel so the panel can focus
          on logs. These fields matter for support tickets and bug reports,
          not for everyday use. */}
      <SettingsSection
        title={t(($) => $.desktop.daemon.diagnostics_title)}
        description={t(($) => $.desktop.daemon.diagnostics_description)}
      >
        <SettingsCard>
          <div className="px-4 py-2">
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.state)}
            value={
              <span className="inline-flex items-center gap-1.5">
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    DAEMON_STATE_COLORS[status.state],
                  )}
                />
                {daemonStateLabel(status.state, t)}
              </span>
            }
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.uptime)}
            value={status.uptime ? formatUptime(status.uptime) : "—"}
          />
          <DiagnosticsRow
            label="PID"
            value={status.pid ?? "—"}
            mono={!!status.pid}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.daemon_id)}
            value={status.daemonId ?? "—"}
            mono={!!status.daemonId}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.profile)}
            value={status.profile || "default"}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.server_url)}
            value={status.serverUrl ?? "—"}
            mono={!!status.serverUrl}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.device_name)}
            value={status.deviceName ?? "—"}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.workspaces)}
            value={
              typeof status.workspaceCount === "number"
                ? status.workspaceCount
                : "—"
            }
          />
          </div>
        </SettingsCard>
      </SettingsSection>
    </SettingsTab>
  );
}
