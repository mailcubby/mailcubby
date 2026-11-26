import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Eye, EyeOff, KeyRound, Pencil, Plus, Power, RefreshCw, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { IntegrationSnippetsPanel } from "@/features/settings/IntegrationSnippetsPanel";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import {
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type ProjectDetail,
  type SMTPCredential,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatExactTimestamp, formatRelativeTime } from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { metaQueryOptions, projectSMTPCredentialsQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

const REVEAL_TTL_MS = 30_000;

function formatTLSLabel(tlsMode: "starttls" | "tls" | "none"): string {
  if (tlsMode === "starttls") return "STARTTLS";
  if (tlsMode === "tls") return "Implicit TLS";
  return "None";
}

const LAST_FIELD_SPAN: Record<number, string> = { 2: "sm:col-span-2", 3: "sm:col-span-3" };

interface ConnectionField {
  label: string;
  /** Undefined while loading. */
  value: string | undefined;
  copyValue?: string | undefined;
}

/** ConnectionFields is the grid of copyable SMTP connection values in the connection card. */
function ConnectionFields({ items, columns }: { items: ConnectionField[]; columns: 2 | 3 }) {
  return (
    <dl
      className={cn(
        "grid grid-cols-1 gap-px border-t border-border bg-border",
        columns === 3 ? "sm:grid-cols-3" : "sm:grid-cols-2",
      )}
    >
      {items.map((item, index) => (
        <div
          key={item.label}
          className={cn(
            "flex min-w-0 flex-col gap-0.5 bg-surface px-5 py-3",
            // The last field fills the rest of its row, so no empty grid cell shows.
            index === items.length - 1 &&
              LAST_FIELD_SPAN[columns - (items.length % columns || columns) + 1],
          )}
        >
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="flex min-w-0 items-center justify-between gap-2 font-mono text-[13px] font-medium text-foreground">
            <span className="truncate" title={item.value}>
              {item.value ?? "…"}
            </span>
            {item.value !== undefined ? (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={`Copy ${item.label}`}
                title={`Copy ${item.label.toLowerCase()}`}
                onClick={() =>
                  void copyToClipboard(item.copyValue ?? item.value ?? "", item.label.toLowerCase())
                }
                className="size-7 shrink-0"
              >
                <Copy className="size-3.5" aria-hidden="true" />
              </Button>
            ) : null}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function ServerSmtpSettings({ project }: { project: ProjectDetail }) {
  const queryClient = useQueryClient();
  const [clock24h] = useClock24hPreference();
  const isOwner = project.role === "owner";

  const { data, isPending, error, refetch } = useQuery(
    projectSMTPCredentialsQueryOptions(project.slug),
  );

  const [selectedCredId, setSelectedCredId] = useState<number | null>(null);
  const [revealedPasswords, setRevealedPasswords] = useState<Record<number, string>>({});
  const [revealingId, setRevealingId] = useState<number | null>(null);
  const [copyingId, setCopyingId] = useState<number | null>(null);
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());

  const [createOpen, setCreateOpen] = useState(false);
  const [createLabel, setCreateLabel] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [createdSecret, setCreatedSecret] = useState<{
    title: string;
    credential: SMTPCredential;
    password: string;
  } | null>(null);

  const [editingCred, setEditingCred] = useState<SMTPCredential | null>(null);
  const [editLabel, setEditLabel] = useState("");
  const [editError, setEditError] = useState<string | null>(null);

  const [rotatingCred, setRotatingCred] = useState<SMTPCredential | null>(null);
  const [deletingCred, setDeletingCred] = useState<SMTPCredential | null>(null);

  const createLabelId = useId();
  const editLabelId = useId();
  const snippetCredSelectId = useId();

  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      for (const timer of timers.values()) {
        clearTimeout(timer);
      }
      timers.clear();
    };
  }, []);

  const items = data?.items ?? [];

  useEffect(() => {
    if (items.length === 0) {
      setSelectedCredId(null);
      return;
    }
    if (selectedCredId === null || !items.some((c) => c.id === selectedCredId)) {
      const firstActive = items.find((c) => !c.disabled) ?? items[0];
      setSelectedCredId(firstActive ? firstActive.id : null);
    }
  }, [items, selectedCredId]);

  const clearRevealed = (id: number) => {
    const existing = timersRef.current.get(id);
    if (existing) {
      clearTimeout(existing);
      timersRef.current.delete(id);
    }
    setRevealedPasswords((prev) => {
      if (!(id in prev)) {
        return prev;
      }
      const next = { ...prev };
      delete next[id];
      return next;
    });
  };

  const rememberRevealed = (id: number, password: string) => {
    const existing = timersRef.current.get(id);
    if (existing) {
      clearTimeout(existing);
    }
    setRevealedPasswords((prev) => ({ ...prev, [id]: password }));
    const timer = setTimeout(() => {
      timersRef.current.delete(id);
      setRevealedPasswords((prev) => {
        if (!(id in prev)) {
          return prev;
        }
        const next = { ...prev };
        delete next[id];
        return next;
      });
    }, REVEAL_TTL_MS);
    timersRef.current.set(id, timer);
  };

  const revealPasswordRequest = async (id: number): Promise<string> => {
    const res = unwrap(
      await api.POST("/api/v1/projects/{project}/smtp-credentials/{id}/reveal", {
        params: { path: { project: project.slug, id } },
      }),
    );
    return res.password;
  };

  const handleToggleReveal = async (cred: SMTPCredential) => {
    if (cred.id in revealedPasswords) {
      clearRevealed(cred.id);
      return;
    }
    setRevealingId(cred.id);
    try {
      const password = await revealPasswordRequest(cred.id);
      rememberRevealed(cred.id, password);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        toast.error("Only project owners can reveal SMTP passwords for this project");
      } else {
        handleApiError(err, "Failed to reveal password");
      }
    } finally {
      setRevealingId(null);
    }
  };

  const handleCopyPassword = async (cred: SMTPCredential) => {
    const cached = revealedPasswords[cred.id];
    if (cached) {
      await copyToClipboard(cached, "SMTP password");
      return;
    }
    setCopyingId(cred.id);
    try {
      const password = await revealPasswordRequest(cred.id);
      await copyToClipboard(password, "SMTP password");
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        toast.error("Only project owners can reveal SMTP passwords for this project");
      } else {
        handleApiError(err, "Failed to copy password");
      }
    } finally {
      setCopyingId(null);
    }
  };

  const createMutation = useMutation({
    mutationFn: async (label: string) =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/smtp-credentials", {
          params: { path: { project: project.slug } },
          body: { label },
        }),
      ),
    onSuccess: async (created) => {
      setCreateOpen(false);
      setCreateLabel("");
      setCreateError(null);
      setSelectedCredId(created.credential.id);
      rememberRevealed(created.credential.id, created.password);
      setCreatedSecret({
        title: "SMTP credential created",
        credential: created.credential,
        password: created.password,
      });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.smtpCredentials(project.slug),
      });
      toast.success(`Created credential "${created.credential.label}"`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details?.label) {
        setCreateError(err.details.label);
        return;
      }
      setCreateError(getApiErrorMessage(err, "Failed to create credential"));
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (vars: { id: number; label?: string; disabled?: boolean }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}/smtp-credentials/{id}", {
          params: { path: { project: project.slug, id: vars.id } },
          body: {
            ...(vars.label !== undefined ? { label: vars.label } : {}),
            ...(vars.disabled !== undefined ? { disabled: vars.disabled } : {}),
          },
        }),
      ),
    onSuccess: async (updated, vars) => {
      if (vars.label !== undefined) {
        setEditingCred(null);
        setEditError(null);
        toast.success(`Renamed credential to "${updated.label}"`);
      } else if (vars.disabled !== undefined) {
        toast.success(
          updated.disabled
            ? `Disabled credential "${updated.label}"`
            : `Enabled credential "${updated.label}"`,
        );
      }
      await queryClient.invalidateQueries({
        queryKey: queryKeys.smtpCredentials(project.slug),
      });
    },
    onError: (err, vars) => {
      if (vars.label !== undefined) {
        setEditError(getApiErrorMessage(err, "Failed to rename credential"));
        return;
      }
      handleApiError(err, "Failed to update credential");
    },
  });

  const rotateMutation = useMutation({
    mutationFn: async (cred: SMTPCredential) =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/smtp-credentials/{id}/rotate", {
          params: { path: { project: project.slug, id: cred.id } },
        }),
      ),
    onSuccess: async (rotated) => {
      setRotatingCred(null);
      rememberRevealed(rotated.credential.id, rotated.password);
      setCreatedSecret({
        title: "Password rotated",
        credential: rotated.credential,
        password: rotated.password,
      });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.smtpCredentials(project.slug),
      });
      toast.success(`Rotated password for "${rotated.credential.label}"`);
    },
    onError: (err) => {
      handleApiError(err, "Failed to rotate credential");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (cred: SMTPCredential) => {
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}/smtp-credentials/{id}", {
          params: { path: { project: project.slug, id: cred.id } },
        }),
      );
      return cred;
    },
    onSuccess: async (deleted) => {
      clearRevealed(deleted.id);
      setDeletingCred(null);
      await queryClient.invalidateQueries({
        queryKey: queryKeys.smtpCredentials(project.slug),
      });
      toast.success(`Deleted credential "${deleted.label}"`);
    },
    onError: (err) => {
      handleApiError(err, "Failed to delete credential");
    },
  });

  const handleCreateSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = createLabel.trim();
    if (!trimmed) {
      setCreateError("Label is required.");
      return;
    }
    setCreateError(null);
    createMutation.mutate(trimmed);
  };

  const handleEditSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!editingCred) return;
    const trimmed = editLabel.trim();
    if (!trimmed) {
      setEditError("Label is required.");
      return;
    }
    setEditError(null);
    updateMutation.mutate({ id: editingCred.id, label: trimmed });
  };

  const selectedCred = useMemo(
    () => items.find((c) => c.id === selectedCredId) ?? items[0] ?? null,
    [items, selectedCredId],
  );

  const connection = data?.connection;
  const revealedSelectedPassword = selectedCred ? revealedPasswords[selectedCred.id] : undefined;

  if (error) {
    return (
      <Card role="alert">
        <CardHeader
          title="Failed to load SMTP credentials"
          description={getApiErrorMessage(error)}
          actions={
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          }
        />
      </Card>
    );
  }

  const openCreate = (label: string) => {
    setCreateLabel(label);
    setCreateError(null);
    setCreateOpen(true);
  };

  return (
    <>
      <Card aria-labelledby="smtp-connection-heading">
        <CardHeader
          titleId="smtp-connection-heading"
          title="SMTP connection"
          description="Use these server settings with any active project credential below."
        />
        <ConnectionFields
          columns={3}
          items={[
            { label: "Host", value: connection?.host },
            { label: "Port", value: connection ? String(connection.port) : undefined },
            {
              label: "TLS mode",
              value: connection ? formatTLSLabel(connection.tls_mode) : undefined,
              copyValue: connection?.tls_mode,
            },
          ]}
        />
      </Card>

      <Card aria-labelledby="smtp-credentials-heading">
        <CardHeader
          titleId="smtp-credentials-heading"
          title="SMTP credentials"
          description="Create separate credentials per environment (up to 20 per project) to filter messages by source."
          actions={
            isOwner ? (
              // One primary per screen: the empty state's "Create first credential" is primary.
              <Button
                variant={!isPending && items.length === 0 ? "outline" : "primary"}
                size="sm"
                disabled={items.length >= 20}
                onClick={() => openCreate("")}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                <span>New credential</span>
              </Button>
            ) : null
          }
        />

        {isPending ? (
          <div className="flex flex-col gap-2 border-t border-border p-5">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-2 border-t border-border px-5 py-10 text-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-accent-subtle text-accent-subtle-foreground">
              <KeyRound className="size-5" aria-hidden="true" />
            </span>
            <p className="text-[13px] font-medium text-foreground">No SMTP credentials yet</p>
            <p className="max-w-sm text-[13px] text-muted-foreground">
              {isOwner
                ? "Create a credential to send mail to this project over authenticated SMTP."
                : "Ask a project owner to create an SMTP credential for this project."}
            </p>
            {isOwner ? (
              <Button
                variant="primary"
                size="sm"
                className="mt-2"
                onClick={() => openCreate("staging")}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                <span>Create first credential</span>
              </Button>
            ) : null}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <Table className="min-w-[600px] table-fixed">
              <colgroup>
                <col className="w-[20%]" />
                <col />
                <col className="w-[136px]" />
                <col className="w-[116px]" />
                {isOwner ? <col className="w-[152px]" /> : null}
              </colgroup>
              <TableHeader>
                <TableRow>
                  <TableHead>Label</TableHead>
                  <TableHead>Username</TableHead>
                  <TableHead>Password</TableHead>
                  <TableHead>Status</TableHead>
                  {isOwner ? (
                    <TableHead className="text-right">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  ) : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((cred) => {
                  const revealed = revealedPasswords[cred.id];
                  const isRevealing = revealingId === cred.id;
                  const isCopying = copyingId === cred.id;
                  return (
                    <TableRow key={cred.id}>
                      <TableCell>
                        <div className="truncate font-medium text-foreground" title={cred.label}>
                          {cred.label}
                        </div>
                        <div
                          className="truncate text-xs text-muted-foreground"
                          title={formatExactTimestamp(cred.created_at, clock24h).combined}
                        >
                          Created {formatRelativeTime(cred.created_at, new Date(), clock24h)}
                        </div>
                      </TableCell>

                      <TableCell>
                        <div className="flex min-w-0 items-center gap-0.5 font-mono text-xs text-foreground">
                          <span className="min-w-0 truncate" title={cred.username}>
                            {cred.username}
                          </span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            aria-label={`Copy username for ${cred.label}`}
                            title="Copy username"
                            onClick={() => void copyToClipboard(cred.username, "SMTP username")}
                            className="size-6 shrink-0"
                          >
                            <Copy className="size-3" aria-hidden="true" />
                          </Button>
                        </div>
                      </TableCell>

                      <TableCell>
                        <div className="flex min-w-0 items-center gap-0.5 font-mono text-xs">
                          <span
                            className="min-w-0 truncate text-foreground"
                            title={revealed ? "Auto-hides after 30 seconds" : "Hidden"}
                          >
                            {revealed ?? "••••••••"}
                          </span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            aria-label={
                              revealed
                                ? `Hide password for ${cred.label}`
                                : `Reveal password for ${cred.label}`
                            }
                            title={revealed ? "Hide password" : "Reveal password (30 s)"}
                            disabled={isRevealing}
                            onClick={() => void handleToggleReveal(cred)}
                            className="size-6 shrink-0"
                          >
                            {revealed ? (
                              <EyeOff className="size-3" aria-hidden="true" />
                            ) : (
                              <Eye className="size-3" aria-hidden="true" />
                            )}
                          </Button>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            aria-label={`Copy password for ${cred.label}`}
                            title="Copy password"
                            disabled={isCopying}
                            onClick={() => void handleCopyPassword(cred)}
                            className="size-6 shrink-0"
                          >
                            <Copy className="size-3" aria-hidden="true" />
                          </Button>
                        </div>
                      </TableCell>

                      <TableCell>
                        <Badge variant={cred.disabled ? "neutral" : "success"} dot>
                          {cred.disabled ? "Disabled" : "Active"}
                        </Badge>
                        <div
                          className="mt-1 truncate text-xs text-muted-foreground tabular-nums"
                          title={
                            cred.last_used_at
                              ? formatExactTimestamp(cred.last_used_at, clock24h).combined
                              : undefined
                          }
                        >
                          {cred.last_used_at
                            ? `Used ${formatRelativeTime(cred.last_used_at, new Date(), clock24h)}`
                            : "Never used"}
                        </div>
                      </TableCell>

                      {isOwner ? (
                        <TableCell className="text-right">
                          <div className="inline-flex items-center justify-end gap-0.5">
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              aria-label={`Rename ${cred.label}`}
                              title="Rename"
                              onClick={() => {
                                setEditingCred(cred);
                                setEditLabel(cred.label);
                                setEditError(null);
                              }}
                              className="size-7"
                            >
                              <Pencil className="size-3.5" aria-hidden="true" />
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              aria-label={`Rotate password for ${cred.label}`}
                              title="Rotate password"
                              onClick={() => setRotatingCred(cred)}
                              className="size-7"
                            >
                              <RefreshCw className="size-3.5" aria-hidden="true" />
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              aria-label={
                                cred.disabled ? `Enable ${cred.label}` : `Disable ${cred.label}`
                              }
                              title={cred.disabled ? "Enable credential" : "Disable credential"}
                              disabled={updateMutation.isPending}
                              onClick={() =>
                                updateMutation.mutate({ id: cred.id, disabled: !cred.disabled })
                              }
                              className="size-7"
                            >
                              <Power className="size-3.5" aria-hidden="true" />
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              aria-label={`Delete ${cred.label}`}
                              title="Delete credential"
                              onClick={() => setDeletingCred(cred)}
                              className="size-7 text-destructive hover:bg-destructive-subtle hover:text-destructive"
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                            </Button>
                          </div>
                        </TableCell>
                      ) : null}
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </Card>

      {connection ? (
        <section aria-labelledby="smtp-snippets-heading" className="flex flex-col gap-3 pt-2">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <div>
              <h2
                id="smtp-snippets-heading"
                className="text-[15px] leading-[22px] font-semibold text-foreground"
              >
                Integration snippets
              </h2>
              <p className="mt-0.5 text-[13px] text-muted-foreground">
                Ready-to-paste configuration populated with your SMTP endpoint and selected
                credential.
              </p>
            </div>
            {items.length > 1 ? (
              <div className="flex items-center gap-2">
                <label
                  htmlFor={snippetCredSelectId}
                  className="text-xs font-medium text-foreground"
                >
                  Credential
                </label>
                <NativeSelect
                  id={snippetCredSelectId}
                  value={selectedCred ? String(selectedCred.id) : ""}
                  onChange={(e) => setSelectedCredId(Number(e.target.value))}
                  wrapperClassName="max-w-64"
                >
                  {items.map((cred) => (
                    <option key={cred.id} value={String(cred.id)}>
                      {cred.label} ({cred.username})
                    </option>
                  ))}
                </NativeSelect>
              </div>
            ) : null}
          </div>

          <IntegrationSnippetsPanel
            input={{
              mode: "server",
              host: connection.host,
              port: connection.port,
              tlsMode: connection.tls_mode,
              username: selectedCred?.username ?? `${project.slug}-<credential>`,
              password: revealedSelectedPassword ?? "********",
            }}
            passwordRevealed={Boolean(revealedSelectedPassword)}
            canRevealPassword={Boolean(selectedCred)}
            revealingPassword={selectedCred !== null && revealingId === selectedCred.id}
            onToggleRevealPassword={
              selectedCred ? () => void handleToggleReveal(selectedCred) : undefined
            }
          />
        </section>
      ) : null}

      <Dialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        title="New SMTP credential"
        description="Give this credential a label (such as staging, preview, or ci) to identify which app sent each message."
      >
        <form onSubmit={handleCreateSubmit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <label htmlFor={createLabelId} className="text-xs font-medium text-foreground">
              Label
            </label>
            <Input
              id={createLabelId}
              autoFocus
              required
              maxLength={80}
              placeholder="staging"
              value={createLabel}
              onChange={(e) => {
                setCreateLabel(e.target.value);
                if (createError) setCreateError(null);
              }}
              aria-invalid={createError ? "true" : undefined}
            />
            {createError ? <p className="text-xs text-destructive">{createError}</p> : null}
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" size="sm" disabled={createMutation.isPending}>
              {createMutation.isPending ? "Creating..." : "Create credential"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={createdSecret !== null}
        onOpenChange={(open) => {
          if (!open) setCreatedSecret(null);
        }}
        title={createdSecret?.title ?? "SMTP credential"}
        description="Copy these credentials into your application. You can reveal the password again later from the credentials table."
      >
        {createdSecret ? (
          <div className="flex flex-col gap-3 text-xs">
            <div className="flex flex-col gap-1">
              <span className="font-medium text-muted-foreground">Username</span>
              <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-background px-3 py-2 font-mono text-foreground">
                <span className="truncate">{createdSecret.credential.username}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={() =>
                    void copyToClipboard(createdSecret.credential.username, "SMTP username")
                  }
                >
                  <Copy className="size-3" aria-hidden="true" />
                  <span>Copy</span>
                </Button>
              </div>
            </div>
            <div className="flex flex-col gap-1">
              <span className="font-medium text-muted-foreground">Password</span>
              <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-background px-3 py-2 font-mono text-foreground">
                <span className="break-all">{createdSecret.password}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={() => void copyToClipboard(createdSecret.password, "SMTP password")}
                >
                  <Copy className="size-3" aria-hidden="true" />
                  <span>Copy</span>
                </Button>
              </div>
            </div>
            <div className="flex justify-end pt-1">
              <Button
                type="button"
                variant="primary"
                size="sm"
                onClick={() => setCreatedSecret(null)}
              >
                Done
              </Button>
            </div>
          </div>
        ) : null}
      </Dialog>

      <Dialog
        open={editingCred !== null}
        onOpenChange={(open) => {
          if (!open) setEditingCred(null);
        }}
        title="Rename SMTP credential"
        description="Renaming updates the label shown in the inbox filter and message details. The SMTP username stays unchanged."
      >
        <form onSubmit={handleEditSubmit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <label htmlFor={editLabelId} className="text-xs font-medium text-foreground">
              Label
            </label>
            <Input
              id={editLabelId}
              autoFocus
              required
              maxLength={80}
              value={editLabel}
              onChange={(e) => {
                setEditLabel(e.target.value);
                if (editError) setEditError(null);
              }}
              aria-invalid={editError ? "true" : undefined}
            />
            {editError ? <p className="text-xs text-destructive">{editError}</p> : null}
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setEditingCred(null)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" size="sm" disabled={updateMutation.isPending}>
              {updateMutation.isPending ? "Saving..." : "Save label"}
            </Button>
          </div>
        </form>
      </Dialog>

      <ConfirmDialog
        open={rotatingCred !== null}
        onOpenChange={(open) => {
          if (!open) setRotatingCred(null);
        }}
        title={rotatingCred ? `Rotate "${rotatingCred.label}" password?` : "Rotate password?"}
        description="Apps using the old password will fail immediately until updated with the new password. The SMTP username stays the same."
        confirmLabel="Rotate password"
        destructive
        pending={rotateMutation.isPending}
        onConfirm={() => {
          if (rotatingCred) rotateMutation.mutate(rotatingCred);
        }}
      />

      <ConfirmDialog
        open={deletingCred !== null}
        onOpenChange={(open) => {
          if (!open) setDeletingCred(null);
        }}
        title={deletingCred ? `Delete "${deletingCred.label}"?` : "Delete credential?"}
        description="SMTP AUTH using this credential will stop working immediately. Existing captured messages remain in the project."
        confirmLabel="Delete credential"
        destructive
        pending={deleteMutation.isPending}
        onConfirm={() => {
          if (deletingCred) deleteMutation.mutate(deletingCred);
        }}
      />
    </>
  );
}

function LocalSmtpSettings({ project }: { project: ProjectDetail }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const host = meta?.smtp.host ?? "localhost";
  const port = meta?.smtp.port ?? 1025;
  const tlsMode = meta?.smtp.tls_mode ?? "none";

  return (
    <>
      <Card aria-labelledby="smtp-local-connection-heading">
        <CardHeader
          titleId="smtp-local-connection-heading"
          title="Local SMTP connection"
          description="In local mode any password is accepted and the SMTP username selects the project."
        />
        <ConnectionFields
          columns={3}
          items={[
            { label: "Host", value: host },
            { label: "Port", value: String(port) },
            { label: "TLS", value: formatTLSLabel(tlsMode), copyValue: tlsMode },
            { label: "Username", value: project.slug },
            { label: "Password", value: "any (e.g. dev)", copyValue: "dev" },
          ]}
        />
      </Card>

      <section aria-labelledby="smtp-snippets-heading" className="flex flex-col gap-3 pt-2">
        <div>
          <h2
            id="smtp-snippets-heading"
            className="text-[15px] leading-[22px] font-semibold text-foreground"
          >
            Integration snippets
          </h2>
          <p className="mt-0.5 text-[13px] text-muted-foreground">
            Copy a ready-to-use configuration for your framework or sendmail binary.
          </p>
        </div>
        <IntegrationSnippetsPanel
          input={{
            mode: "local",
            host,
            port,
            tlsMode,
            username: project.slug,
            password: "dev",
          }}
        />
      </section>
    </>
  );
}

export function SmtpSettingsSection({ projectSlug }: { projectSlug: string }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";

  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="smtp"
      title="SMTP & snippets"
      description={
        isServer
          ? "Manage per-project SMTP credentials and copy framework integration snippets."
          : "Local mode SMTP connection details and framework integration snippets."
      }
    >
      {(project) =>
        isServer ? (
          <ServerSmtpSettings project={project} />
        ) : (
          <LocalSmtpSettings project={project} />
        )
      }
    </ProjectSettingsLayout>
  );
}
