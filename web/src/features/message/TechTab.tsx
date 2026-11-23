import { AlertTriangle, Copy, ShieldCheck, ShieldOff } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { MessageDetail } from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatBytes, formatCount, formatExactTimestamp } from "@/lib/format";

export interface TechTabProps {
  message: MessageDetail;
}

export function TechTab({ message }: TechTabProps) {
  const receivedExact = formatExactTimestamp(message.received_at);
  const dateHeaderExact = message.date_header ? formatExactTimestamp(message.date_header) : null;
  const envelope = message.envelope;
  const warnings = message.parse_warnings ?? [];

  return (
    <div className="h-full overflow-auto bg-background p-4">
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        <section className="rounded-lg border border-border bg-surface">
          <div className="border-b border-border px-4 py-2.5">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              SMTP Envelope &amp; Transport
            </h3>
          </div>
          {envelope ? (
            <dl className="divide-y divide-border text-xs">
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">MAIL FROM</dt>
                <dd className="font-mono break-all text-foreground sm:col-span-2">
                  {envelope.mail_from || "<>"}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">RCPT TO</dt>
                <dd className="flex flex-col gap-0.5 font-mono break-all text-foreground sm:col-span-2">
                  {envelope.rcpt_to.length > 0 ? (
                    envelope.rcpt_to.map((rcpt) => <span key={rcpt}>{rcpt}</span>)
                  ) : (
                    <span className="text-muted-foreground">(none)</span>
                  )}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">Remote IP</dt>
                <dd className="font-mono text-foreground sm:col-span-2">
                  {envelope.remote_ip ?? "—"}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">HELO / EHLO</dt>
                <dd className="font-mono text-foreground sm:col-span-2">{envelope.helo ?? "—"}</dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">TLS</dt>
                <dd className="sm:col-span-2">
                  {envelope.tls ? (
                    <span className="inline-flex items-center gap-1 font-medium text-foreground">
                      <ShieldCheck className="size-3.5 text-accent" aria-hidden="true" />
                      Yes
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-muted-foreground">
                      <ShieldOff className="size-3.5" aria-hidden="true" />
                      No (plaintext)
                    </span>
                  )}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">AUTH User</dt>
                <dd className="font-mono text-foreground sm:col-span-2">
                  {envelope.auth_user ?? "—"}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
                <dt className="text-muted-foreground">Credential</dt>
                <dd className="text-foreground sm:col-span-2">
                  {message.credential_label ? (
                    <Badge variant="outline">{message.credential_label}</Badge>
                  ) : (
                    "—"
                  )}
                </dd>
              </div>
            </dl>
          ) : (
            <p className="px-4 py-3 text-xs text-muted-foreground">
              Envelope metadata is not available for this role.
            </p>
          )}
        </section>

        <section className="rounded-lg border border-border bg-surface">
          <div className="border-b border-border px-4 py-2.5">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Message Metadata
            </h3>
          </div>
          <dl className="divide-y divide-border text-xs">
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Mailcubby ID</dt>
              <dd className="flex items-center justify-between gap-2 font-mono text-foreground sm:col-span-2">
                <span className="break-all">{message.id}</span>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Copy Mailcubby ID"
                  onClick={() => void copyToClipboard(message.id, "message ID")}
                  className="size-6 shrink-0"
                >
                  <Copy className="size-3" aria-hidden="true" />
                </Button>
              </dd>
            </div>
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Message-ID</dt>
              <dd className="flex items-center justify-between gap-2 font-mono text-foreground sm:col-span-2">
                <span className="break-all">{message.message_id_header ?? "—"}</span>
                {message.message_id_header ? (
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Copy Message-ID header"
                    onClick={() =>
                      void copyToClipboard(message.message_id_header ?? "", "Message-ID")
                    }
                    className="size-6 shrink-0"
                  >
                    <Copy className="size-3" aria-hidden="true" />
                  </Button>
                ) : null}
              </dd>
            </div>
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Received at</dt>
              <dd className="text-foreground tabular-nums sm:col-span-2">
                <div>{receivedExact.local}</div>
                <div className="text-muted-foreground">{receivedExact.utc}</div>
              </dd>
            </div>
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Date header</dt>
              <dd className="text-foreground tabular-nums sm:col-span-2">
                {dateHeaderExact ? (
                  <>
                    <div>{dateHeaderExact.local}</div>
                    <div className="text-muted-foreground">{dateHeaderExact.utc}</div>
                  </>
                ) : (
                  <span className="text-muted-foreground">(missing or invalid)</span>
                )}
              </dd>
            </div>
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Raw size</dt>
              <dd className="text-foreground tabular-nums sm:col-span-2">
                {formatBytes(message.size_bytes)} ({formatCount(message.size_bytes)} bytes)
              </dd>
            </div>
            <div className="grid grid-cols-1 gap-1 px-4 py-2.5 sm:grid-cols-3">
              <dt className="text-muted-foreground">Parts</dt>
              <dd className="flex flex-wrap gap-1.5 sm:col-span-2">
                <Badge variant={message.has_html ? "secondary" : "outline"}>
                  HTML: {message.has_html ? "Yes" : "No"}
                </Badge>
                <Badge variant={message.has_text ? "secondary" : "outline"}>
                  Text: {message.has_text ? (message.text_generated ? "Generated" : "Yes") : "No"}
                </Badge>
                <Badge variant="outline">
                  Attachments: {formatCount(message.attachment_count)}
                </Badge>
              </dd>
            </div>
          </dl>
        </section>

        <section className="rounded-lg border border-border bg-surface">
          <div className="border-b border-border px-4 py-2.5">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              MIME Parse Warnings ({warnings.length})
            </h3>
          </div>
          {warnings.length === 0 ? (
            <p className="px-4 py-3 text-xs text-muted-foreground">
              No parse warnings recorded for this message.
            </p>
          ) : (
            <ul className="divide-y divide-border text-xs">
              {warnings.map((warning) => (
                <li key={warning} className="flex items-start gap-2 px-4 py-2.5 text-foreground">
                  <AlertTriangle
                    className="mt-0.5 size-3.5 shrink-0 text-amber-500"
                    aria-hidden="true"
                  />
                  <span className="font-mono break-all">{warning}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
