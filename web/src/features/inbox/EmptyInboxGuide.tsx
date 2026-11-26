import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Copy, Eye, EyeOff, Inbox, KeyRound, Send } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/components/EmptyState";
import { Button } from "@/components/ui/button";
import { IntegrationSnippetsPanel } from "@/features/settings/IntegrationSnippetsPanel";
import {
  ApiError,
  api,
  handleApiError,
  type ProjectDetail,
  type ProjectRole,
  unwrap,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { metaQueryOptions, projectSMTPCredentialsQueryOptions } from "@/lib/queries";

export interface EmptyInboxGuideProps {
  project: ProjectDetail;
  effectiveRole?: ProjectRole;
  onSentTest: () => Promise<void>;
}

export function EmptyInboxGuide({ project, effectiveRole, onSentTest }: EmptyInboxGuideProps) {
  const navigate = useNavigate();
  const { data: meta } = useQuery(metaQueryOptions());
  const role = effectiveRole ?? project.role;
  const isServer = meta?.mode === "server";
  const isViewer = role === "viewer";

  const { data: credsData } = useQuery({
    ...projectSMTPCredentialsQueryOptions(project.slug),
    enabled: isServer && !isViewer,
  });

  const [revealed, setRevealed] = useState<{ slug: string; password: string } | null>(null);
  const [revealing, setRevealing] = useState(false);
  const revealTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const revealedPassword = revealed?.slug === project.slug ? revealed.password : null;

  useEffect(() => {
    return () => {
      if (revealTimerRef.current) {
        clearTimeout(revealTimerRef.current);
      }
    };
  }, []);

  const sendTestMutation = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/send-test", {
          params: { path: { project: project.slug } },
        }),
      ),
    onSuccess: async (created) => {
      toast.success("Test email sent");
      await onSentTest();
      void navigate({
        to: "/p/$project/m/$messageId",
        params: { project: project.slug, messageId: created.id },
      });
    },
  });

  if (isViewer) {
    return (
      <div className="flex h-full items-center justify-center">
        <EmptyState
          icon={Inbox}
          title="No messages yet"
          description={`Emails sent to ${project.name} appear here as they arrive.`}
        />
      </div>
    );
  }

  const mode = meta?.mode ?? "local";
  const activeCred = credsData?.items.find((c) => !c.disabled) ?? credsData?.items[0] ?? null;
  const host = credsData?.connection.host ?? meta?.smtp.host ?? "localhost";
  const port = credsData?.connection.port ?? meta?.smtp.port ?? 1025;
  const tlsMode = credsData?.connection.tls_mode ?? meta?.smtp.tls_mode ?? "none";
  const username = isServer
    ? (activeCred?.username ?? `${project.slug}-<credential>`)
    : project.slug;
  const password = isServer ? (revealedPassword ?? "********") : "dev";

  const handleToggleReveal = async () => {
    if (!activeCred) return;
    if (revealedPassword) {
      setRevealed(null);
      if (revealTimerRef.current) {
        clearTimeout(revealTimerRef.current);
        revealTimerRef.current = null;
      }
      return;
    }
    setRevealing(true);
    try {
      const res = unwrap(
        await api.POST("/api/v1/projects/{project}/smtp-credentials/{id}/reveal", {
          params: { path: { project: project.slug, id: activeCred.id } },
        }),
      );
      setRevealed({ slug: project.slug, password: res.password });
      if (revealTimerRef.current) clearTimeout(revealTimerRef.current);
      revealTimerRef.current = setTimeout(() => {
        setRevealed(null);
        revealTimerRef.current = null;
      }, 30_000);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        toast.error("Only project owners can reveal SMTP passwords for this project");
      } else {
        handleApiError(err, "Failed to reveal SMTP password");
      }
    } finally {
      setRevealing(false);
    }
  };

  const showRecipientRouting =
    Boolean(meta?.inbox_domain) &&
    (!isServer ||
      ("recipient_routing_enabled" in project.settings &&
        project.settings.recipient_routing_enabled));

  return (
    <div className="h-full overflow-y-auto bg-background px-6 pb-8">
      <div className="mx-auto flex max-w-2xl flex-col gap-5">
        <EmptyState
          icon={Inbox}
          title="No messages yet"
          description="Point your app's SMTP client at Mailcubby, or send a sample message now."
          className="pt-10 pb-2"
          action={
            <Button
              variant="primary"
              size="sm"
              disabled={sendTestMutation.isPending}
              onClick={() => sendTestMutation.mutate()}
            >
              <Send className="size-3.5" aria-hidden="true" />
              <span>{sendTestMutation.isPending ? "Sending..." : "Send test email"}</span>
            </Button>
          }
        />

        <section className="rounded-lg border border-border bg-surface">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2.5">
            <h3 className="text-[13px] font-semibold text-foreground">
              SMTP connection{" "}
              <span className="font-normal text-muted-foreground">
                · {isServer ? "Server mode" : "Local mode"}
              </span>
            </h3>
            {isServer ? (
              <Link
                to="/p/$project/settings/smtp"
                params={{ project: project.slug }}
                className="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline"
              >
                <KeyRound className="size-3" aria-hidden="true" />
                <span>{activeCred ? "Manage credentials" : "Create SMTP credential"}</span>
              </Link>
            ) : null}
          </div>
          <dl className="grid grid-cols-1 divide-y divide-border text-xs sm:grid-cols-2 sm:divide-y-0">
            {[
              { label: "Host", value: host, copyValue: host },
              { label: "Port", value: String(port), copyValue: String(port) },
              { label: "TLS", value: tlsMode, copyValue: tlsMode },
              {
                label: "Username",
                value: username,
                copyValue: activeCred ? username : !isServer ? username : undefined,
              },
            ].map((row) => (
              <div
                key={row.label}
                className="flex items-center justify-between gap-2 border-b border-border px-4 py-2.5 sm:odd:border-r"
              >
                <dt className="text-muted-foreground">{row.label}</dt>
                <dd className="flex min-w-0 items-center gap-1.5 font-mono text-foreground">
                  <span className="truncate">{row.value}</span>
                  {row.copyValue ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Copy ${row.label}`}
                      onClick={() => {
                        if (row.copyValue) {
                          void copyToClipboard(row.copyValue, row.label);
                        }
                      }}
                      className="size-6 shrink-0"
                    >
                      <Copy className="size-3" aria-hidden="true" />
                    </Button>
                  ) : null}
                </dd>
              </div>
            ))}
            <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-2.5 sm:col-span-2 sm:border-b-0">
              <dt className="text-muted-foreground">Password</dt>
              <dd className="flex min-w-0 items-center gap-1.5 font-mono text-foreground">
                <span className="truncate">
                  {isServer ? (revealedPassword ?? "********") : "any (e.g. dev)"}
                </span>
                {isServer && activeCred ? (
                  <>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={revealedPassword ? "Hide password" : "Reveal password"}
                      disabled={revealing}
                      onClick={() => void handleToggleReveal()}
                      className="size-6 shrink-0"
                    >
                      {revealedPassword ? (
                        <EyeOff className="size-3" aria-hidden="true" />
                      ) : (
                        <Eye className="size-3" aria-hidden="true" />
                      )}
                    </Button>
                    {revealedPassword ? (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label="Copy Password"
                        onClick={() => void copyToClipboard(revealedPassword, "SMTP password")}
                        className="size-6 shrink-0"
                      >
                        <Copy className="size-3" aria-hidden="true" />
                      </Button>
                    ) : null}
                  </>
                ) : !isServer ? (
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Copy Password"
                    onClick={() => void copyToClipboard("dev", "Password")}
                    className="size-6 shrink-0"
                  >
                    <Copy className="size-3" aria-hidden="true" />
                  </Button>
                ) : null}
              </dd>
            </div>
          </dl>
        </section>

        {showRecipientRouting && meta?.inbox_domain ? (
          <section className="rounded-lg border border-border bg-surface p-4 text-xs">
            <h3 className="font-semibold text-foreground">Recipient routing addresses</h3>
            <p className="mt-1 text-muted-foreground">
              Send without SMTP AUTH to either address format to route directly into{" "}
              <code className="font-mono text-foreground">{project.slug}</code>:
            </p>
            <div className="mt-2.5 flex flex-wrap gap-2 font-mono">
              {[
                `anything@${project.slug}.${meta.inbox_domain}`,
                `${project.slug}@${meta.inbox_domain}`,
                `${project.slug}+tag@${meta.inbox_domain}`,
              ].map((addr) => (
                <button
                  key={addr}
                  type="button"
                  onClick={() => void copyToClipboard(addr, "routing address")}
                  className="inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1 text-foreground hover:bg-muted"
                >
                  <span>{addr}</span>
                  <Copy className="size-3 text-muted-foreground" aria-hidden="true" />
                </button>
              ))}
            </div>
          </section>
        ) : null}

        <IntegrationSnippetsPanel
          input={{
            mode,
            host,
            port,
            tlsMode,
            username,
            password,
          }}
          passwordRevealed={Boolean(revealedPassword)}
          canRevealPassword={Boolean(isServer && activeCred)}
          revealingPassword={revealing}
          onToggleRevealPassword={activeCred ? () => void handleToggleReveal() : undefined}
        />
      </div>
    </div>
  );
}
