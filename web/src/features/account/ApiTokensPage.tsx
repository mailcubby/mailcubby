import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AlertTriangle, Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { AccountLayout } from "@/features/account/AccountLayout";
import {
  type APITokenInfo,
  type APITokenScope,
  ApiError,
  api,
  type CreateAPITokenRequest,
  getApiErrorMessage,
  handleApiError,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatExactTimestamp, formatRelativeTime } from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { apiTokensQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

const EXPIRY_PRESETS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "", label: "Never expires" },
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
  { value: "custom", label: "Custom days..." },
];

export interface ApiTokensPageProps {
  initialProjectSlug?: string | undefined;
}

export function ApiTokensPage({ initialProjectSlug }: ApiTokensPageProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [clock24h] = useClock24hPreference();

  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";

  const {
    data: tokens,
    isPending,
    error,
    refetch,
  } = useQuery({
    ...apiTokensQueryOptions(),
    enabled: isServer,
  });
  const { data: projects } = useQuery({
    ...projectsQueryOptions(),
    enabled: isServer,
  });

  const [createOpen, setCreateOpen] = useState(Boolean(initialProjectSlug));
  const [name, setName] = useState("");
  const [scope, setScope] = useState<APITokenScope>("read");
  const [projectSlug, setProjectSlug] = useState(initialProjectSlug ?? "");
  const [expiryPreset, setExpiryPreset] = useState("");
  const [customDays, setCustomDays] = useState("30");
  const [formError, setFormError] = useState<string | null>(null);

  const [createdToken, setCreatedToken] = useState<{
    info: APITokenInfo;
    raw: string;
  } | null>(null);
  const [revokingToken, setRevokingToken] = useState<APITokenInfo | null>(null);

  const nameId = useId();
  const scopeId = useId();
  const projectId = useId();
  const expiryId = useId();
  const customDaysId = useId();

  useEffect(() => {
    const instanceName = meta?.instance_name || "Mailcubby";
    document.title = `API tokens · ${instanceName}`;
  }, [meta?.instance_name]);

  useEffect(() => {
    if (meta && meta.mode !== "server") {
      void navigate({ to: "/account/preferences", replace: true });
    }
  }, [meta, navigate]);

  useEffect(() => {
    if (initialProjectSlug) {
      setProjectSlug(initialProjectSlug);
      setCreateOpen(true);
    }
  }, [initialProjectSlug]);

  const createMutation = useMutation({
    mutationFn: async (body: CreateAPITokenRequest) =>
      unwrap(
        await api.POST("/api/v1/tokens", {
          body,
        }),
      ),
    onSuccess: async (res) => {
      setCreateOpen(false);
      setName("");
      setScope("read");
      setProjectSlug("");
      setExpiryPreset("");
      setFormError(null);
      setCreatedToken({ info: res.token_info, raw: res.token });
      await queryClient.invalidateQueries({ queryKey: queryKeys.apiTokens() });
      toast.success(`Created API token "${res.token_info.name}"`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setFormError(first);
          return;
        }
      }
      setFormError(getApiErrorMessage(err, "Failed to create API token"));
    },
  });

  const revokeMutation = useMutation({
    mutationFn: async (tok: APITokenInfo) => {
      unwrapVoid(
        await api.DELETE("/api/v1/tokens/{id}", {
          params: { path: { id: tok.id } },
        }),
      );
      return tok;
    },
    onSuccess: async (revoked) => {
      setRevokingToken(null);
      await queryClient.invalidateQueries({ queryKey: queryKeys.apiTokens() });
      toast.success(`Revoked API token "${revoked.name}"`);
    },
    onError: (err) => {
      handleApiError(err, "Failed to revoke API token");
    },
  });

  const handleCreateSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setFormError("Token name is required.");
      return;
    }

    let expiresInDays: number | undefined;
    if (expiryPreset === "custom") {
      const parsed = Number.parseInt(customDays.trim(), 10);
      if (!Number.isFinite(parsed) || parsed < 1 || parsed > 3650) {
        setFormError("Custom expiry must be between 1 and 3650 days.");
        return;
      }
      expiresInDays = parsed;
    } else if (expiryPreset !== "") {
      expiresInDays = Number.parseInt(expiryPreset, 10);
    }

    setFormError(null);
    createMutation.mutate({
      name: trimmedName,
      scope,
      ...(projectSlug ? { project: projectSlug } : {}),
      ...(expiresInDays !== undefined ? { expires_in_days: expiresInDays } : {}),
    });
  };

  const nowMs = Date.now();

  return (
    <AccountLayout
      section="tokens"
      title="API tokens"
      description="Personal bearer tokens for CI pipelines, Playwright tests, and REST API scripts."
    >
      <div className="flex flex-col gap-5">
        <section className="rounded-lg border border-border bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-3.5">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Personal API tokens</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Tokens inherit the minimum of their scope and your current project role. Management
                endpoints are never accessible via tokens.
              </p>
            </div>
            <Button
              size="sm"
              onClick={() => {
                setFormError(null);
                setCreateOpen(true);
              }}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              <span>New API token</span>
            </Button>
          </div>

          {error ? (
            <div className="p-5 text-xs text-muted-foreground">
              <p className="font-medium text-foreground">Could not load API tokens</p>
              <p className="mt-1">{getApiErrorMessage(error)}</p>
              <Button variant="outline" size="xs" className="mt-3" onClick={() => void refetch()}>
                Retry
              </Button>
            </div>
          ) : isPending ? (
            <div className="p-5 text-xs text-muted-foreground">Loading API tokens...</div>
          ) : !tokens || tokens.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 px-5 py-10 text-center">
              <KeyRound className="size-6 text-muted-foreground/70" aria-hidden="true" />
              <p className="text-xs font-medium text-foreground">No API tokens yet</p>
              <p className="max-w-sm text-xs text-muted-foreground">
                Create a token to authenticate REST API requests with{" "}
                <code className="font-mono text-foreground">Authorization: Bearer mailcubby_…</code>
                .
              </p>
              <Button
                size="sm"
                className="mt-1"
                onClick={() => {
                  setFormError(null);
                  setCreateOpen(true);
                }}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                <span>Create first token</span>
              </Button>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-xs">
                <thead>
                  <tr className="border-b border-border bg-background/50 text-[11px] font-medium text-muted-foreground">
                    <th className="px-4 py-2">Name</th>
                    <th className="px-4 py-2">Prefix</th>
                    <th className="px-4 py-2">Scope</th>
                    <th className="px-4 py-2">Project</th>
                    <th className="px-4 py-2">Last used</th>
                    <th className="px-4 py-2">Expires</th>
                    <th className="px-4 py-2 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {tokens.map((tok) => {
                    const isExpired =
                      tok.expires_at !== null && new Date(tok.expires_at).getTime() <= nowMs;
                    return (
                      <tr key={tok.id} className="hover:bg-muted/30">
                        <td className="px-4 py-2.5 align-middle">
                          <div className="font-medium text-foreground">{tok.name}</div>
                          <div
                            className="text-[11px] text-muted-foreground"
                            title={formatExactTimestamp(tok.created_at, clock24h).combined}
                          >
                            Created {formatRelativeTime(tok.created_at, new Date(), clock24h)}
                          </div>
                        </td>
                        <td className="px-4 py-2.5 align-middle font-mono text-foreground">
                          mailcubby_{tok.prefix}…
                        </td>
                        <td className="px-4 py-2.5 align-middle">
                          <Badge variant={tok.scope === "write" ? "default" : "outline"}>
                            {tok.scope}
                          </Badge>
                        </td>
                        <td className="px-4 py-2.5 align-middle">
                          {tok.project ? (
                            <code className="rounded-xs border border-border bg-background px-1.5 py-0.5 font-mono text-[11px] text-foreground">
                              {tok.project}
                            </code>
                          ) : (
                            <span className="text-muted-foreground">All projects</span>
                          )}
                        </td>
                        <td
                          className="px-4 py-2.5 align-middle tabular-nums text-muted-foreground"
                          title={
                            tok.last_used_at
                              ? formatExactTimestamp(tok.last_used_at, clock24h).combined
                              : undefined
                          }
                        >
                          {tok.last_used_at
                            ? formatRelativeTime(tok.last_used_at, new Date(), clock24h)
                            : "Never"}
                        </td>
                        <td className="px-4 py-2.5 align-middle tabular-nums">
                          {tok.expires_at ? (
                            isExpired ? (
                              <Badge variant="destructive">Expired</Badge>
                            ) : (
                              <span
                                className="text-muted-foreground"
                                title={formatExactTimestamp(tok.expires_at, clock24h).combined}
                              >
                                {new Intl.DateTimeFormat(undefined, {
                                  dateStyle: "medium",
                                }).format(new Date(tok.expires_at))}
                              </span>
                            )
                          ) : (
                            <span className="text-muted-foreground">Never</span>
                          )}
                        </td>
                        <td className="px-4 py-2.5 align-middle text-right">
                          <Button
                            type="button"
                            variant="ghost"
                            size="xs"
                            aria-label={`Revoke ${tok.name}`}
                            onClick={() => setRevokingToken(tok)}
                            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                          >
                            <Trash2 className="size-3" aria-hidden="true" />
                            <span>Revoke</span>
                          </Button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>

      <Dialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        title="New API token"
        description="Generate a personal bearer token. The raw token is shown once immediately after creation."
      >
        <form onSubmit={handleCreateSubmit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <label htmlFor={nameId} className="text-xs font-medium text-foreground">
              Name
            </label>
            <Input
              id={nameId}
              autoFocus
              required
              maxLength={80}
              placeholder="CI Playwright tests"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (formError) setFormError(null);
              }}
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor={scopeId} className="text-xs font-medium text-foreground">
              Scope
            </label>
            <select
              id={scopeId}
              value={scope}
              onChange={(e) => setScope(e.target.value as APITokenScope)}
              className="h-8 rounded-md border border-border bg-background px-2.5 text-xs text-foreground"
            >
              <option value="read">read — list, search, read, wait, SSE</option>
              <option value="write">write — read plus mark seen, tags, delete, send test</option>
            </select>
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor={projectId} className="text-xs font-medium text-foreground">
              Project restriction
            </label>
            <select
              id={projectId}
              value={projectSlug}
              onChange={(e) => setProjectSlug(e.target.value)}
              className="h-8 rounded-md border border-border bg-background px-2.5 text-xs text-foreground"
            >
              <option value="">All accessible projects</option>
              {projects?.map((proj) => (
                <option key={proj.id} value={proj.slug}>
                  {proj.name} ({proj.slug})
                </option>
              ))}
            </select>
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor={expiryId} className="text-xs font-medium text-foreground">
              Expiration
            </label>
            <select
              id={expiryId}
              value={expiryPreset}
              onChange={(e) => setExpiryPreset(e.target.value)}
              className="h-8 rounded-md border border-border bg-background px-2.5 text-xs text-foreground"
            >
              {EXPIRY_PRESETS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          {expiryPreset === "custom" ? (
            <div className="flex flex-col gap-1.5">
              <label htmlFor={customDaysId} className="text-xs font-medium text-foreground">
                Expires in (days)
              </label>
              <Input
                id={customDaysId}
                type="number"
                min={1}
                max={3650}
                value={customDays}
                onChange={(e) => setCustomDays(e.target.value)}
              />
            </div>
          ) : null}

          {formError ? <p className="text-xs text-destructive">{formError}</p> : null}

          <div className="flex justify-end gap-2 pt-1">
            <Button type="button" variant="outline" size="sm" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={createMutation.isPending}>
              {createMutation.isPending ? "Creating..." : "Create token"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={createdToken !== null}
        onOpenChange={(open) => {
          if (!open) setCreatedToken(null);
        }}
        title="API token created"
        description="Copy your token now. For security, Mailcubby stores only its SHA-256 hash and cannot display it again."
        closeOnBackdropClick={false}
      >
        {createdToken ? (
          <div className="flex flex-col gap-4 text-xs">
            <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-amber-800 dark:text-amber-300">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
              <span>This token is shown once. Store it in your CI secrets manager now.</span>
            </div>

            <div className="flex items-center justify-between gap-2 rounded-md border border-border bg-background px-3 py-2.5 font-mono text-foreground">
              <span className="break-all" data-testid="created-api-token">
                {createdToken.raw}
              </span>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => void copyToClipboard(createdToken.raw, "API token")}
              >
                <Copy className="size-3" aria-hidden="true" />
                <span>Copy</span>
              </Button>
            </div>

            <div className="flex justify-end">
              <Button type="button" size="sm" onClick={() => setCreatedToken(null)}>
                Done
              </Button>
            </div>
          </div>
        ) : null}
      </Dialog>

      <ConfirmDialog
        open={revokingToken !== null}
        onOpenChange={(open) => {
          if (!open) setRevokingToken(null);
        }}
        title={revokingToken ? `Revoke "${revokingToken.name}"?` : "Revoke API token?"}
        description="Any script or CI job using this token will immediately receive 401 Unauthorized."
        confirmLabel="Revoke token"
        destructive
        pending={revokeMutation.isPending}
        onConfirm={() => {
          if (revokingToken) revokeMutation.mutate(revokingToken);
        }}
      />
    </AccountLayout>
  );
}
