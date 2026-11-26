import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import {
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type ProjectDetail,
  type ProjectSettings,
  type RetentionDefaults,
  unwrap,
} from "@/lib/api";
import { formatBytes, formatCount, formatRetentionAge } from "@/lib/format";
import { queryKeys } from "@/lib/queryKeys";
import {
  type AgeUnit,
  combineAgeSeconds,
  combineStorageBytes,
  parseNonNegativeInt,
  type SizeUnit,
  splitAgeSeconds,
  splitStorageBytes,
} from "@/lib/retentionUnits";
import { cn } from "@/lib/utils";

const FALLBACK_DEFAULTS: RetentionDefaults = {
  retention_max_age: 30 * 86_400,
  retention_max_messages: 5_000,
  retention_max_bytes: 1024 * 1024 * 1024,
};

function isOwnerSettings(settings: ProjectDetail["settings"]): settings is ProjectSettings {
  return (
    "retention_max_age" in settings ||
    "retention_max_messages" in settings ||
    "retention_max_bytes" in settings ||
    "recipient_routing_enabled" in settings
  );
}

function UsageBar({
  label,
  current,
  limit,
  currentText,
  limitText,
}: {
  label: string;
  current: number;
  limit: number;
  currentText: string;
  limitText: string;
}) {
  const hasLimit = limit > 0;
  const rawPct = hasLimit ? Math.round((current / limit) * 100) : 0;
  const clampedPct = Math.min(100, Math.max(0, rawPct));

  return (
    <div className="rounded-md border border-border bg-background/60 p-3.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        {hasLimit ? (
          <span className="text-xs font-medium text-foreground tabular-nums">{rawPct}%</span>
        ) : (
          <span className="text-xs text-muted-foreground">Unlimited</span>
        )}
      </div>
      <div className="mt-1.5 text-sm font-semibold text-foreground tabular-nums">
        {hasLimit ? `${currentText} / ${limitText}` : currentText}
      </div>
      {hasLimit ? (
        <div
          role="progressbar"
          aria-label={`${label} retention usage`}
          aria-valuemin={0}
          aria-valuemax={limit}
          aria-valuenow={current}
          className="mt-2.5 h-1.5 w-full overflow-hidden rounded-full bg-muted"
        >
          <div
            className={cn(
              "h-full rounded-full transition-all",
              rawPct >= 100 ? "bg-destructive" : rawPct >= 80 ? "bg-warning" : "bg-accent",
            )}
            style={{ width: `${clampedPct}%` }}
          />
        </div>
      ) : null}
    </div>
  );
}

function RetentionSettingsForm({ project }: { project: ProjectDetail }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const inheritAgeId = useId();
  const ageValId = useId();
  const ageUnitId = useId();
  const inheritMessagesId = useId();
  const messagesValId = useId();
  const inheritBytesId = useId();
  const bytesValId = useId();
  const bytesUnitId = useId();

  useEffect(() => {
    if (project.role !== "owner") {
      void navigate({
        to: "/p/$project/settings/smtp",
        params: { project: project.slug },
        replace: true,
      });
    }
  }, [project.role, project.slug, navigate]);

  const ownerSettings = isOwnerSettings(project.settings) ? project.settings : null;
  const defaults = project.retention_defaults ?? FALLBACK_DEFAULTS;

  const savedAge = ownerSettings?.retention_max_age ?? null;
  const savedMessages = ownerSettings?.retention_max_messages ?? null;
  const savedBytes = ownerSettings?.retention_max_bytes ?? null;

  const [inheritAge, setInheritAge] = useState(savedAge === null);
  const [ageValue, setAgeValue] = useState(
    () => splitAgeSeconds(savedAge ?? defaults.retention_max_age).value,
  );
  const [ageUnit, setAgeUnit] = useState<AgeUnit>(
    () => splitAgeSeconds(savedAge ?? defaults.retention_max_age).unit,
  );

  const [inheritMessages, setInheritMessages] = useState(savedMessages === null);
  const [maxMessages, setMaxMessages] = useState(() =>
    String(savedMessages ?? defaults.retention_max_messages),
  );

  const [inheritBytes, setInheritBytes] = useState(savedBytes === null);
  const [sizeValue, setSizeValue] = useState(
    () => splitStorageBytes(savedBytes ?? defaults.retention_max_bytes).value,
  );
  const [sizeUnit, setSizeUnit] = useState<SizeUnit>(
    () => splitStorageBytes(savedBytes ?? defaults.retention_max_bytes).unit,
  );

  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    const nextAge = splitAgeSeconds(savedAge ?? defaults.retention_max_age);
    const nextSize = splitStorageBytes(savedBytes ?? defaults.retention_max_bytes);
    setInheritAge(savedAge === null);
    setAgeValue(nextAge.value);
    setAgeUnit(nextAge.unit);
    setInheritMessages(savedMessages === null);
    setMaxMessages(String(savedMessages ?? defaults.retention_max_messages));
    setInheritBytes(savedBytes === null);
    setSizeValue(nextSize.value);
    setSizeUnit(nextSize.unit);
    setFormError(null);
  }, [
    savedAge,
    savedMessages,
    savedBytes,
    defaults.retention_max_age,
    defaults.retention_max_messages,
    defaults.retention_max_bytes,
  ]);

  const updateMutation = useMutation({
    mutationFn: async (patch: {
      retention_max_age: number | null;
      retention_max_messages: number | null;
      retention_max_bytes: number | null;
    }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}", {
          params: { path: { project: project.slug } },
          body: { settings: patch },
        }),
      ),
    onSuccess: async (updated) => {
      setFormError(null);
      queryClient.setQueryData(queryKeys.project(project.slug), updated);
      await queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
      toast.success("Retention settings saved");
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setFormError(first);
          return;
        }
      }
      setFormError(getApiErrorMessage(err, "Failed to update retention settings"));
      handleApiError(err, "Failed to update retention settings");
    },
  });

  if (project.role !== "owner" || !ownerSettings) {
    return null;
  }

  const nextAgeVal = inheritAge ? null : combineAgeSeconds(ageValue, ageUnit);
  const nextMessagesVal = inheritMessages ? null : parseNonNegativeInt(maxMessages);
  const nextBytesVal = inheritBytes ? null : combineStorageBytes(sizeValue, sizeUnit);

  const hasChanges =
    (inheritAge ? null : nextAgeVal) !== savedAge ||
    (inheritMessages ? null : nextMessagesVal) !== savedMessages ||
    (inheritBytes ? null : nextBytesVal) !== savedBytes;

  const effectiveAge = savedAge ?? defaults.retention_max_age;
  const effectiveMessages = savedMessages ?? defaults.retention_max_messages;
  const effectiveBytes = savedBytes ?? defaults.retention_max_bytes;

  const handleSave = (e: FormEvent) => {
    e.preventDefault();
    if (!inheritAge && nextAgeVal === null) {
      setFormError("Max age must be a non-negative number (0 = unlimited).");
      return;
    }
    if (!inheritMessages && nextMessagesVal === null) {
      setFormError("Max messages must be a non-negative integer (0 = unlimited).");
      return;
    }
    if (!inheritBytes && nextBytesVal === null) {
      setFormError("Max storage size must be a non-negative number (0 = unlimited).");
      return;
    }
    setFormError(null);
    updateMutation.mutate({
      retention_max_age: inheritAge ? null : nextAgeVal,
      retention_max_messages: inheritMessages ? null : nextMessagesVal,
      retention_max_bytes: inheritBytes ? null : nextBytesVal,
    });
  };

  const handleReset = () => {
    const resetAge = splitAgeSeconds(savedAge ?? defaults.retention_max_age);
    const resetSize = splitStorageBytes(savedBytes ?? defaults.retention_max_bytes);
    setInheritAge(savedAge === null);
    setAgeValue(resetAge.value);
    setAgeUnit(resetAge.unit);
    setInheritMessages(savedMessages === null);
    setMaxMessages(String(savedMessages ?? defaults.retention_max_messages));
    setInheritBytes(savedBytes === null);
    setSizeValue(resetSize.value);
    setSizeUnit(resetSize.unit);
    setFormError(null);
  };

  const defaultAgeLabel = formatRetentionAge(defaults.retention_max_age);
  const defaultMessagesLabel =
    defaults.retention_max_messages > 0
      ? `${formatCount(defaults.retention_max_messages)} messages`
      : "Unlimited";
  const defaultBytesLabel =
    defaults.retention_max_bytes > 0 ? formatBytes(defaults.retention_max_bytes) : "Unlimited";

  return (
    <>
      <Card aria-labelledby="usage-heading">
        <CardHeader
          titleId="usage-heading"
          title="Current usage"
          description="Oldest messages are pruned automatically whenever any active limit is exceeded."
        />
        <CardContent>
          <div className="grid gap-4 sm:grid-cols-2">
            <UsageBar
              label="Messages"
              current={project.message_count}
              limit={effectiveMessages}
              currentText={formatCount(project.message_count)}
              limitText={`${formatCount(effectiveMessages)} messages`}
            />
            <UsageBar
              label="Storage"
              current={project.size_bytes}
              limit={effectiveBytes}
              currentText={formatBytes(project.size_bytes)}
              limitText={formatBytes(effectiveBytes)}
            />
          </div>

          <p className="mt-3 text-xs text-muted-foreground">
            Age policy:{" "}
            <span className="font-medium text-foreground">
              {effectiveAge > 0
                ? `Keep messages for ${formatRetentionAge(effectiveAge)}`
                : "No age limit"}
            </span>
          </p>
        </CardContent>
      </Card>

      <Card aria-labelledby="limits-heading">
        <CardHeader
          titleId="limits-heading"
          title="Retention limits"
          description={
            <>
              Inherit instance defaults or set per-project overrides. Enter{" "}
              <code className="font-mono text-foreground">0</code> to disable a specific limit.
            </>
          }
        />

        <form onSubmit={handleSave}>
          <CardContent className="flex flex-col divide-y divide-border">
            <div className="flex flex-col gap-3 pb-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <div className="text-[13px] font-medium text-foreground">Max age</div>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    Remove messages older than this threshold.
                  </p>
                </div>
                <label
                  htmlFor={inheritAgeId}
                  className="inline-flex cursor-pointer items-center gap-2 text-xs text-foreground"
                >
                  <input
                    id={inheritAgeId}
                    type="checkbox"
                    checked={inheritAge}
                    onChange={(e) => {
                      const checked = e.target.checked;
                      setInheritAge(checked);
                      if (!checked) {
                        const next = splitAgeSeconds(defaults.retention_max_age);
                        setAgeValue(next.value);
                        setAgeUnit(next.unit);
                      }
                      setFormError(null);
                    }}
                    className="size-3.5 rounded-sm border border-border accent-accent"
                  />
                  <span>Inherit instance default ({defaultAgeLabel})</span>
                </label>
              </div>
              {!inheritAge ? (
                <div className="flex items-center gap-2">
                  <label htmlFor={ageValId} className="sr-only">
                    Max age
                  </label>
                  <Input
                    id={ageValId}
                    type="number"
                    min={0}
                    step="any"
                    value={ageValue}
                    onChange={(e) => {
                      setAgeValue(e.target.value);
                      if (formError) {
                        setFormError(null);
                      }
                    }}
                    className="w-28 tabular-nums"
                  />
                  <label htmlFor={ageUnitId} className="sr-only">
                    Max age unit
                  </label>
                  <NativeSelect
                    id={ageUnitId}
                    value={ageUnit}
                    onChange={(e) => setAgeUnit(e.target.value as AgeUnit)}
                  >
                    <option value="days">days</option>
                    <option value="hours">hours</option>
                  </NativeSelect>
                  <span className="text-xs text-muted-foreground">0 = unlimited</span>
                </div>
              ) : null}
            </div>

            <div className="flex flex-col gap-3 py-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <div className="text-[13px] font-medium text-foreground">Max messages</div>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    Keep at most this many newest messages in the project.
                  </p>
                </div>
                <label
                  htmlFor={inheritMessagesId}
                  className="inline-flex cursor-pointer items-center gap-2 text-xs text-foreground"
                >
                  <input
                    id={inheritMessagesId}
                    type="checkbox"
                    checked={inheritMessages}
                    onChange={(e) => {
                      const checked = e.target.checked;
                      setInheritMessages(checked);
                      if (!checked) {
                        setMaxMessages(String(defaults.retention_max_messages));
                      }
                      setFormError(null);
                    }}
                    className="size-3.5 rounded-sm border border-border accent-accent"
                  />
                  <span>Inherit instance default ({defaultMessagesLabel})</span>
                </label>
              </div>
              {!inheritMessages ? (
                <div className="flex items-center gap-2">
                  <label htmlFor={messagesValId} className="sr-only">
                    Max messages
                  </label>
                  <Input
                    id={messagesValId}
                    type="number"
                    min={0}
                    step={1}
                    value={maxMessages}
                    onChange={(e) => {
                      setMaxMessages(e.target.value);
                      if (formError) {
                        setFormError(null);
                      }
                    }}
                    className="w-32 tabular-nums"
                  />
                  <span className="text-xs text-muted-foreground">messages (0 = unlimited)</span>
                </div>
              ) : null}
            </div>

            <div className="flex flex-col gap-3 pt-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <div className="text-[13px] font-medium text-foreground">Max storage size</div>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    Cap total raw message bytes retained in the project.
                  </p>
                </div>
                <label
                  htmlFor={inheritBytesId}
                  className="inline-flex cursor-pointer items-center gap-2 text-xs text-foreground"
                >
                  <input
                    id={inheritBytesId}
                    type="checkbox"
                    checked={inheritBytes}
                    onChange={(e) => {
                      const checked = e.target.checked;
                      setInheritBytes(checked);
                      if (!checked) {
                        const next = splitStorageBytes(defaults.retention_max_bytes);
                        setSizeValue(next.value);
                        setSizeUnit(next.unit);
                      }
                      setFormError(null);
                    }}
                    className="size-3.5 rounded-sm border border-border accent-accent"
                  />
                  <span>Inherit instance default ({defaultBytesLabel})</span>
                </label>
              </div>
              {!inheritBytes ? (
                <div className="flex items-center gap-2">
                  <label htmlFor={bytesValId} className="sr-only">
                    Max storage size
                  </label>
                  <Input
                    id={bytesValId}
                    type="number"
                    min={0}
                    step="any"
                    value={sizeValue}
                    onChange={(e) => {
                      setSizeValue(e.target.value);
                      if (formError) {
                        setFormError(null);
                      }
                    }}
                    className="w-28 tabular-nums"
                  />
                  <label htmlFor={bytesUnitId} className="sr-only">
                    Max storage size unit
                  </label>
                  <NativeSelect
                    id={bytesUnitId}
                    value={sizeUnit}
                    onChange={(e) => setSizeUnit(e.target.value as SizeUnit)}
                  >
                    <option value="KB">KB</option>
                    <option value="MB">MB</option>
                    <option value="GB">GB</option>
                  </NativeSelect>
                  <span className="text-xs text-muted-foreground">0 = unlimited</span>
                </div>
              ) : null}
            </div>

            {formError ? (
              <p role="alert" className="pt-4 text-xs font-medium text-destructive">
                {formError}
              </p>
            ) : null}
          </CardContent>

          <CardFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!hasChanges || updateMutation.isPending}
              onClick={handleReset}
            >
              Reset
            </Button>
            <Button
              type="submit"
              variant="primary"
              size="sm"
              disabled={!hasChanges || updateMutation.isPending}
            >
              {updateMutation.isPending ? "Saving..." : "Save changes"}
            </Button>
          </CardFooter>
        </form>
      </Card>
    </>
  );
}

export function RetentionSettingsSection({ projectSlug }: { projectSlug: string }) {
  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="retention"
      title="Retention"
      description="Control how long messages are kept and view project storage usage against limits."
    >
      {(project) => <RetentionSettingsForm project={project} />}
    </ProjectSettingsLayout>
  );
}
