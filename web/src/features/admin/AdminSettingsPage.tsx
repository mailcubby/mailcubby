import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { AdminLayout } from "@/features/admin/AdminLayout";
import {
  type AdminSettingsResponse,
  type AdminUpdateSettingsRequest,
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type SettingSource,
  unwrap,
} from "@/lib/api";
import { adminSettingsQueryOptions, authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
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

function SourceBadge({ source }: { source: SettingSource }) {
  return source === "override" ? (
    <Badge variant="accent">Overridden</Badge>
  ) : (
    <Badge variant="outline">From env</Badge>
  );
}

function AdminSettingsForm({ data }: { data: AdminSettingsResponse }) {
  const queryClient = useQueryClient();

  const nameId = useId();
  const ageId = useId();
  const ageUnitId = useId();
  const messagesId = useId();
  const bytesId = useId();
  const bytesUnitId = useId();

  const initialAge = splitAgeSeconds(data.effective.retention_max_age);
  const initialSize = splitStorageBytes(data.effective.retention_max_bytes);

  const [instanceName, setInstanceName] = useState(data.effective.instance_name);
  const [ageValue, setAgeValue] = useState(initialAge.value);
  const [ageUnit, setAgeUnit] = useState<AgeUnit>(initialAge.unit);
  const [maxMessages, setMaxMessages] = useState(String(data.effective.retention_max_messages));
  const [sizeValue, setSizeValue] = useState(initialSize.value);
  const [sizeUnit, setSizeUnit] = useState<SizeUnit>(initialSize.unit);
  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    const nextAge = splitAgeSeconds(data.effective.retention_max_age);
    const nextSize = splitStorageBytes(data.effective.retention_max_bytes);
    setInstanceName(data.effective.instance_name);
    setAgeValue(nextAge.value);
    setAgeUnit(nextAge.unit);
    setMaxMessages(String(data.effective.retention_max_messages));
    setSizeValue(nextSize.value);
    setSizeUnit(nextSize.unit);
    setFormError(null);
  }, [data]);

  const updateMutation = useMutation({
    mutationFn: async (body: AdminUpdateSettingsRequest) =>
      unwrap(await api.PATCH("/api/v1/admin/settings", { body })),
    onSuccess: async (updated) => {
      setFormError(null);
      queryClient.setQueryData(queryKeys.adminSettings(), updated);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.meta() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
      ]);
      toast.success("Instance settings saved");
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setFormError(first);
          return;
        }
      }
      const msg = getApiErrorMessage(err, "Failed to update settings");
      setFormError(msg);
      handleApiError(err, "Failed to update settings");
    },
  });

  const parsedAge = combineAgeSeconds(ageValue, ageUnit);
  const parsedMessages = parseNonNegativeInt(maxMessages);
  const parsedBytes = combineStorageBytes(sizeValue, sizeUnit);
  const trimmedName = instanceName.trim();

  const nameChanged = trimmedName !== data.effective.instance_name;
  const ageChanged = parsedAge !== null && parsedAge !== data.effective.retention_max_age;
  const messagesChanged =
    parsedMessages !== null && parsedMessages !== data.effective.retention_max_messages;
  const bytesChanged = parsedBytes !== null && parsedBytes !== data.effective.retention_max_bytes;
  const hasChanges = nameChanged || ageChanged || messagesChanged || bytesChanged;

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!trimmedName || trimmedName.length > 80) {
      setFormError("Instance name must be between 1 and 80 characters.");
      return;
    }
    if (parsedAge === null) {
      setFormError("Default max age must be a non-negative number (0 = unlimited).");
      return;
    }
    if (parsedMessages === null) {
      setFormError("Default max messages must be a non-negative integer (0 = unlimited).");
      return;
    }
    if (parsedBytes === null) {
      setFormError("Default max storage size must be a non-negative number (0 = unlimited).");
      return;
    }

    const body: AdminUpdateSettingsRequest = {};
    if (nameChanged) {
      body.instance_name = trimmedName;
    }
    if (ageChanged) {
      body.retention_max_age = parsedAge;
    }
    if (messagesChanged) {
      body.retention_max_messages = parsedMessages;
    }
    if (bytesChanged) {
      body.retention_max_bytes = parsedBytes;
    }
    if (Object.keys(body).length === 0) {
      return;
    }
    setFormError(null);
    updateMutation.mutate(body);
  };

  const resetFieldToEnv = (field: keyof AdminUpdateSettingsRequest) => {
    setFormError(null);
    updateMutation.mutate({ [field]: null });
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-6">
      <Card className="p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">
              Instance identity
            </h2>
            <p className="mt-0.5 text-[13px] text-muted-foreground">
              Shown in the header, page titles, and login screen. Database overrides take precedence
              over <code className="font-mono text-foreground">MAILCUBBY_INSTANCE_NAME</code>.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <SourceBadge source={data.source.instance_name} />
            {data.source.instance_name === "override" ? (
              <Button
                type="button"
                variant="ghost"
                size="xs"
                disabled={updateMutation.isPending}
                onClick={() => resetFieldToEnv("instance_name")}
              >
                <RotateCcw className="size-3" aria-hidden="true" />
                <span>Reset to env</span>
              </Button>
            ) : null}
          </div>
        </div>

        <div className="mt-4 max-w-md">
          <label htmlFor={nameId} className="block text-xs font-medium text-foreground">
            Instance name
          </label>
          <Input
            id={nameId}
            required
            maxLength={80}
            value={instanceName}
            onChange={(e) => {
              setInstanceName(e.target.value);
              if (formError) {
                setFormError(null);
              }
            }}
            className="mt-1.5"
          />
        </div>
      </Card>

      <Card className="p-5">
        <div>
          <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">
            Default retention policies
          </h2>
          <p className="mt-0.5 text-[13px] text-muted-foreground">
            Applied to every project that inherits instance defaults. Set a limit to{" "}
            <code className="font-mono text-foreground">0</code> to disable it by default.
          </p>
        </div>

        <div className="mt-5 flex flex-col divide-y divide-border">
          <div className="flex flex-col gap-3 pb-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <label htmlFor={ageId} className="text-xs font-medium text-foreground">
                  Default max age
                </label>
                <SourceBadge source={data.source.retention_max_age} />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                Messages older than this are swept automatically (
                <code className="font-mono">MAILCUBBY_RETENTION_MAX_AGE</code>).
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Input
                id={ageId}
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
                className="w-24 tabular-nums"
              />
              <label htmlFor={ageUnitId} className="sr-only">
                Default max age unit
              </label>
              <NativeSelect
                id={ageUnitId}
                value={ageUnit}
                onChange={(e) => setAgeUnit(e.target.value as AgeUnit)}
              >
                <option value="days">days</option>
                <option value="hours">hours</option>
              </NativeSelect>
              {data.source.retention_max_age === "override" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  aria-label="Reset max age to environment default"
                  title="Reset max age to environment default"
                  disabled={updateMutation.isPending}
                  onClick={() => resetFieldToEnv("retention_max_age")}
                >
                  <RotateCcw className="size-3" aria-hidden="true" />
                  <span className="sr-only sm:not-sr-only">Reset</span>
                </Button>
              ) : null}
            </div>
          </div>

          <div className="flex flex-col gap-3 py-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <label htmlFor={messagesId} className="text-xs font-medium text-foreground">
                  Default max messages
                </label>
                <SourceBadge source={data.source.retention_max_messages} />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                Maximum messages retained per project (
                <code className="font-mono">MAILCUBBY_RETENTION_MAX_MESSAGES</code>).
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Input
                id={messagesId}
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
                className="w-28 tabular-nums"
              />
              {data.source.retention_max_messages === "override" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  aria-label="Reset max messages to environment default"
                  title="Reset max messages to environment default"
                  disabled={updateMutation.isPending}
                  onClick={() => resetFieldToEnv("retention_max_messages")}
                >
                  <RotateCcw className="size-3" aria-hidden="true" />
                  <span className="sr-only sm:not-sr-only">Reset</span>
                </Button>
              ) : null}
            </div>
          </div>

          <div className="flex flex-col gap-3 pt-4 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <label htmlFor={bytesId} className="text-xs font-medium text-foreground">
                  Default max storage size
                </label>
                <SourceBadge source={data.source.retention_max_bytes} />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                Maximum raw message bytes per project (
                <code className="font-mono">MAILCUBBY_RETENTION_MAX_BYTES</code>).
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Input
                id={bytesId}
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
                className="w-24 tabular-nums"
              />
              <label htmlFor={bytesUnitId} className="sr-only">
                Default max storage size unit
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
              {data.source.retention_max_bytes === "override" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  aria-label="Reset max size to environment default"
                  title="Reset max size to environment default"
                  disabled={updateMutation.isPending}
                  onClick={() => resetFieldToEnv("retention_max_bytes")}
                >
                  <RotateCcw className="size-3" aria-hidden="true" />
                  <span className="sr-only sm:not-sr-only">Reset</span>
                </Button>
              ) : null}
            </div>
          </div>
        </div>

        {formError ? (
          <p role="alert" className="mt-4 text-xs font-medium text-destructive">
            {formError}
          </p>
        ) : null}

        <div className="mt-5 flex items-center justify-end gap-2 border-t border-border pt-4">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={!hasChanges || updateMutation.isPending}
            onClick={() => {
              const resetAge = splitAgeSeconds(data.effective.retention_max_age);
              const resetSize = splitStorageBytes(data.effective.retention_max_bytes);
              setInstanceName(data.effective.instance_name);
              setAgeValue(resetAge.value);
              setAgeUnit(resetAge.unit);
              setMaxMessages(String(data.effective.retention_max_messages));
              setSizeValue(resetSize.value);
              setSizeUnit(resetSize.unit);
              setFormError(null);
            }}
          >
            Discard
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={!hasChanges || updateMutation.isPending}
          >
            {updateMutation.isPending ? "Saving..." : "Save settings"}
          </Button>
        </div>
      </Card>
    </form>
  );
}

export function AdminSettingsPage() {
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());
  const isAdmin = Boolean(meta?.mode === "server" && me?.user.is_admin);

  const settingsQuery = useQuery({
    ...adminSettingsQueryOptions(),
    enabled: isAdmin,
  });

  return (
    <AdminLayout
      section="settings"
      title="Instance settings"
      description="Configure instance identity and default retention limits across all projects."
    >
      {settingsQuery.isPending ? (
        <div className="flex flex-col gap-4">
          <Skeleton className="h-36 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      ) : settingsQuery.error ? (
        <div
          role="alert"
          className="rounded-lg border border-destructive/40 bg-destructive-subtle p-5 text-[13px] text-destructive"
        >
          Failed to load instance settings: {getApiErrorMessage(settingsQuery.error)}
        </div>
      ) : (
        <AdminSettingsForm data={settingsQuery.data} />
      )}
    </AdminLayout>
  );
}
