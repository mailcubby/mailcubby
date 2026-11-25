import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Eye, EyeOff, KeyRound, Pencil, Plus, Power, RefreshCw, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
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

const REVEAL_TTL_MS = 30_000;

function formatTLSLabel(tlsMode: "starttls" | "tls" | "none"): string {
  if (tlsMode === "starttls") return "STARTTLS";
  if (tlsMode === "tls") return "Implicit TLS";
  return "None";
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
      <div className="rounded-lg border border-border bg-surface p-5 text-xs text-muted-foreground">
        <p className="font-medium text-foreground">Failed to load SMTP credentials</p>
        <p className="mt-1">{getApiErrorMessage(error)}</p>
        <Button variant="outline" size="xs" className="mt-3" onClick={() => void refetch()}>
          Retry
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <section className="rounded-lg border border-border bg-surface">
        <div className="border-b border-border px-5 py-3">
          <h2 className="text-sm font-semibold text-foreground">SMTP connection</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Use these server settings with any active project credential below.
          </p>
        </div>
        <dl className="grid grid-cols-1 divide-y divide-border text-xs sm:grid-cols-3 sm:divide-x sm:divide-y-0">
          {[
            { label: "Host", value: connection?.host ?? "…" },
            { label: "Port", value: connection ? String(connection.port) : "…" },
            {
              label: "TLS mode",
              value: connection ? formatTLSLabel(connection.tls_mode) : "…",
              copyValue: connection?.tls_mode,
            },
          ].map((item) => (
            <div key={item.label} className="flex flex-col gap-0.5 px-5 py-3">
              <dt className="text-[11px] text-muted-foreground">{item.label}</dt>
              <dd className="flex min-w-0 items-center justify-between gap-2 font-mono text-xs font-medium text-foreground">
                <span className="truncate">{item.value}</span>
                {connection ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={`Copy ${item.label}`}
                    onClick={() =>
                      void copyToClipboard(item.copyValue ?? item.value, item.label.toLowerCase())
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
      </section>

      <section className="rounded-lg border border-border bg-surface">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-3.5">
          <div>
            <h2 className="text-sm font-semibold text-foreground">SMTP credentials</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Create separate credentials per environment (up to 20 per project) to filter messages
              by source.
            </p>
          </div>
          {isOwner ? (
            <Button
              size="sm"
              disabled={items.length >= 20}
              onClick={() => {
                setCreateLabel("");
                setCreateError(null);
                setCreateOpen(true);
              }}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              <span>New credential</span>
            </Button>
          ) : null}
        </div>

        {isPending ? (
          <div className="p-5 text-xs text-muted-foreground">Loading credentials...</div>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-2 px-5 py-8 text-center">
            <KeyRound className="size-6 text-muted-foreground/70" aria-hidden="true" />
            <p className="text-xs font-medium text-foreground">No SMTP credentials yet</p>
            <p className="max-w-sm text-xs text-muted-foreground">
              {isOwner
                ? "Create a credential to send mail to this project over authenticated SMTP."
                : "Ask a project owner to create an SMTP credential for this project."}
            </p>
            {isOwner ? (
              <Button
                size="sm"
                className="mt-1"
                onClick={() => {
                  setCreateLabel("staging");
                  setCreateError(null);
                  setCreateOpen(true);
                }}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                <span>Create first credential</span>
              </Button>
            ) : null}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-xs">
              <thead>
                <tr className="border-b border-border bg-background/50 text-[11px] font-medium text-muted-foreground">
                  <th className="px-4 py-2">Label</th>
                  <th className="px-4 py-2">Username</th>
                  <th className="px-4 py-2">Password</th>
                  <th className="px-4 py-2">Last used</th>
                  <th className="px-4 py-2">Status</th>
                  {isOwner ? <th className="px-4 py-2 text-right">Actions</th> : null}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {items.map((cred) => {
                  const revealed = revealedPasswords[cred.id];
                  const isRevealing = revealingId === cred.id;
                  const isCopying = copyingId === cred.id;
                  return (
                    <tr key={cred.id} className="hover:bg-muted/30">
                      <td className="px-4 py-2.5 align-middle font-medium text-foreground">
                        <div className="flex items-center gap-1.5">
                          <span>{cred.label}</span>
                        </div>
                        <div
                          className="text-[11px] font-normal text-muted-foreground"
                          title={formatExactTimestamp(cred.created_at, clock24h).combined}
                        >
                          Created {formatRelativeTime(cred.created_at, new Date(), clock24h)}
                        </div>
                      </td>

                      <td className="px-4 py-2.5 align-middle">
                        <div className="flex items-center gap-1 font-mono text-foreground">
                          <span className="max-w-44 truncate" title={cred.username}>
                            {cred.username}
                          </span>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            aria-label={`Copy username for ${cred.label}`}
                            onClick={() => void copyToClipboard(cred.username, "SMTP username")}
                            className="size-6 shrink-0"
                          >
                            <Copy className="size-3" aria-hidden="true" />
                          </Button>
                        </div>
                      </td>

                      <td className="px-4 py-2.5 align-middle">
                        <div className="flex items-center gap-1 font-mono">
                          <span
                            className="max-w-36 truncate text-foreground"
                            title={revealed ? "Auto-hides after 30 seconds" : "Hidden"}
                          >
                            {revealed ?? "••••••••••••"}
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
                      </td>

                      <td
                        className="px-4 py-2.5 align-middle tabular-nums text-muted-foreground"
                        title={
                          cred.last_used_at
                            ? formatExactTimestamp(cred.last_used_at, clock24h).combined
                            : undefined
                        }
                      >
                        {cred.last_used_at
                          ? formatRelativeTime(cred.last_used_at, new Date(), clock24h)
                          : "Never"}
                      </td>

                      <td className="px-4 py-2.5 align-middle">
                        <Badge variant={cred.disabled ? "outline" : "default"}>
                          {cred.disabled ? "Disabled" : "Active"}
                        </Badge>
                      </td>

                      {isOwner ? (
                        <td className="px-4 py-2.5 align-middle text-right">
                          <div className="inline-flex items-center justify-end gap-1">
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
                              className="size-7 text-destructive hover:bg-destructive/10 hover:text-destructive"
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                            </Button>
                          </div>
                        </td>
                      ) : null}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {connection ? (
        <section className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Integration snippets</h2>
              <p className="text-xs text-muted-foreground">
                Ready-to-paste configuration populated with your SMTP endpoint and selected
                credential.
              </p>
            </div>
            {items.length > 1 ? (
              <div className="flex items-center gap-2">
                <label htmlFor={snippetCredSelectId} className="text-xs text-muted-foreground">
                  Credential
                </label>
                <select
                  id={snippetCredSelectId}
                  value={selectedCred ? String(selectedCred.id) : ""}
                  onChange={(e) => setSelectedCredId(Number(e.target.value))}
                  className="h-8 rounded-md border border-border bg-surface px-2.5 text-xs text-foreground"
                >
                  {items.map((cred) => (
                    <option key={cred.id} value={String(cred.id)}>
                      {cred.label} ({cred.username})
                    </option>
                  ))}
                </select>
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
            <Button type="submit" size="sm" disabled={createMutation.isPending}>
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
              <Button type="button" size="sm" onClick={() => setCreatedSecret(null)}>
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
            <Button type="submit" size="sm" disabled={updateMutation.isPending}>
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
    </div>
  );
}

function LocalSmtpSettings({ project }: { project: ProjectDetail }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const host = meta?.smtp.host ?? "localhost";
  const port = meta?.smtp.port ?? 1025;
  const tlsMode = meta?.smtp.tls_mode ?? "none";

  return (
    <div className="flex flex-col gap-6">
      <section className="rounded-lg border border-border bg-surface">
        <div className="border-b border-border px-5 py-3">
          <h2 className="text-sm font-semibold text-foreground">Local SMTP connection</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            In local mode any password is accepted and the SMTP username selects the project.
          </p>
        </div>
        <dl className="grid grid-cols-1 divide-y divide-border text-xs sm:grid-cols-2 sm:divide-y-0">
          {[
            { label: "Host", value: host },
            { label: "Port", value: String(port) },
            { label: "TLS", value: formatTLSLabel(tlsMode), copyValue: tlsMode },
            { label: "Username", value: project.slug },
            { label: "Password", value: "any (e.g. dev)", copyValue: "dev" },
          ].map((row) => (
            <div
              key={row.label}
              className="flex items-center justify-between gap-2 border-b border-border px-5 py-2.5 sm:odd:border-r"
            >
              <dt className="text-muted-foreground">{row.label}</dt>
              <dd className="flex min-w-0 items-center gap-1.5 font-mono text-foreground">
                <span className="truncate">{row.value}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={`Copy ${row.label}`}
                  onClick={() =>
                    void copyToClipboard(row.copyValue ?? row.value, row.label.toLowerCase())
                  }
                  className="size-6 shrink-0"
                >
                  <Copy className="size-3" aria-hidden="true" />
                </Button>
              </dd>
            </div>
          ))}
        </dl>
      </section>

      <section className="flex flex-col gap-3">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Integration snippets</h2>
          <p className="text-xs text-muted-foreground">
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
    </div>
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
