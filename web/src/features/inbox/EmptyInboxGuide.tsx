import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Copy, Inbox, Send } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { generateIntegrationSnippets } from "@/features/settings/snippets";
import { api, type ProjectDetail, type ProjectRole, unwrap } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { metaQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export interface EmptyInboxGuideProps {
  project: ProjectDetail;
  effectiveRole?: ProjectRole;
  onSentTest: () => Promise<void>;
}

export function EmptyInboxGuide({ project, effectiveRole, onSentTest }: EmptyInboxGuideProps) {
  const navigate = useNavigate();
  const { data: meta } = useQuery(metaQueryOptions());
  const [activeSnippetId, setActiveSnippetId] = useState("laravel");

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

  const mode = meta?.mode ?? "local";
  const host = meta?.smtp.host ?? "localhost";
  const port = meta?.smtp.port ?? 1025;
  const tlsMode = meta?.smtp.tls_mode ?? "none";
  const username = project.slug;
  const password = mode === "local" ? "dev" : "********";

  const snippets = useMemo(
    () =>
      generateIntegrationSnippets({
        mode,
        host,
        port,
        tlsMode,
        username,
        password,
      }),
    [mode, host, port, tlsMode, username, password],
  );

  const activeSnippet = snippets.find((s) => s.id === activeSnippetId) ?? snippets[0];

  if ((effectiveRole ?? project.role) === "viewer") {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
        <Inbox className="size-8 opacity-50" aria-hidden="true" />
        <h2 className="text-sm font-medium text-foreground">No messages yet</h2>
        <p className="max-w-sm text-xs text-muted-foreground">
          Messages captured for {project.name} will appear here.
        </p>
      </div>
    );
  }

  return (
    <div className="h-full overflow-y-auto bg-background p-6">
      <div className="mx-auto flex max-w-2xl flex-col gap-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-1">
            <h2 className="text-base font-semibold text-foreground">No messages yet</h2>
            <p className="text-[13px] text-muted-foreground">
              Point your application&apos;s SMTP client at Mailcubby, or send a built-in sample
              message right now.
            </p>
          </div>
          <Button
            variant="default"
            size="sm"
            disabled={sendTestMutation.isPending}
            onClick={() => sendTestMutation.mutate()}
          >
            <Send className="size-3.5" aria-hidden="true" />
            <span>{sendTestMutation.isPending ? "Sending..." : "Send test email"}</span>
          </Button>
        </div>

        <section className="rounded-lg border border-border bg-surface">
          <div className="border-b border-border px-4 py-2.5">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              SMTP Connection ({mode === "local" ? "Local mode" : "Server mode"})
            </h3>
          </div>
          <dl className="grid grid-cols-1 divide-y divide-border text-xs sm:grid-cols-2 sm:divide-y-0">
            {[
              { label: "Host", value: host },
              { label: "Port", value: String(port) },
              { label: "TLS", value: tlsMode },
              { label: "Username", value: username },
              {
                label: "Password",
                value: mode === "local" ? "any (e.g. dev)" : "********",
                copyValue: mode === "local" ? "dev" : undefined,
              },
            ].map((row) => (
              <div
                key={row.label}
                className="flex items-center justify-between gap-2 border-b border-border px-4 py-2.5 sm:odd:border-r"
              >
                <dt className="text-muted-foreground">{row.label}</dt>
                <dd className="flex min-w-0 items-center gap-1.5 font-mono text-foreground">
                  <span className="truncate">{row.value}</span>
                  {(row.copyValue ?? row.value) ? (
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Copy ${row.label}`}
                      onClick={() => void copyToClipboard(row.copyValue ?? row.value, row.label)}
                      className="size-6 shrink-0"
                    >
                      <Copy className="size-3" aria-hidden="true" />
                    </Button>
                  ) : null}
                </dd>
              </div>
            ))}
          </dl>
        </section>

        {meta?.inbox_domain ? (
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

        {activeSnippet ? (
          <section className="overflow-hidden rounded-lg border border-border bg-surface">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-1.5">
              <div
                role="tablist"
                aria-label="Integration snippets"
                className="flex flex-wrap items-center gap-1"
              >
                {snippets.map((snippet) => {
                  const selected = snippet.id === activeSnippet.id;
                  return (
                    <button
                      key={snippet.id}
                      type="button"
                      role="tab"
                      aria-selected={selected}
                      onClick={() => setActiveSnippetId(snippet.id)}
                      className={cn(
                        "cursor-pointer rounded-sm px-2 py-1 text-xs font-medium transition-colors",
                        selected
                          ? "bg-muted text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      {snippet.label}
                    </button>
                  );
                })}
              </div>

              <Button
                variant="outline"
                size="xs"
                onClick={() =>
                  void copyToClipboard(activeSnippet.code, `${activeSnippet.label} snippet`)
                }
              >
                <Copy className="size-3" aria-hidden="true" />
                <span>Copy snippet</span>
              </Button>
            </div>
            <pre className="overflow-x-auto p-4 font-mono text-xs leading-5 text-foreground">
              <code>{activeSnippet.code}</code>
            </pre>
          </section>
        ) : null}
      </div>
    </div>
  );
}
