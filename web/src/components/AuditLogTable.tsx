import { ChevronDown, ChevronRight } from "lucide-react";
import { Fragment, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { AuditEntry } from "@/lib/api";
import { formatExactTimestamp, formatRelativeTime } from "@/lib/format";

export const ALL_AUDIT_ACTIONS: ReadonlyArray<string> = [
  "setup.complete",
  "auth.login",
  "auth.login_failed",
  "auth.logout",
  "user.create",
  "user.update",
  "user.disable",
  "user.enable",
  "user.delete",
  "user.password_reset_link",
  "user.password_change",
  "project.create",
  "project.update",
  "project.delete",
  "project.admin_access",
  "member.add",
  "member.update",
  "member.remove",
  "invite.create",
  "invite.revoke",
  "invite.accept",
  "smtp_credential.create",
  "smtp_credential.reveal",
  "smtp_credential.rotate",
  "smtp_credential.update",
  "smtp_credential.delete",
  "smtp.auth_lockout",
  "api_token.create",
  "api_token.revoke",
  "messages.delete_all",
  "settings.update",
];

export const PROJECT_AUDIT_ACTIONS: ReadonlyArray<string> = [
  "project.create",
  "project.update",
  "project.admin_access",
  "member.add",
  "member.update",
  "member.remove",
  "invite.create",
  "invite.revoke",
  "invite.accept",
  "smtp_credential.create",
  "smtp_credential.reveal",
  "smtp_credential.rotate",
  "smtp_credential.update",
  "smtp_credential.delete",
  "smtp.auth_lockout",
  "messages.delete_all",
];

export interface AuditLogTableProps {
  entries: AuditEntry[];
  isPending: boolean;
  showProject?: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
  emptyMessage?: string;
}

export function AuditLogTable({
  entries,
  isPending,
  showProject = false,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  emptyMessage = "No audit events match the current filters.",
}: AuditLogTableProps) {
  const [expandedIds, setExpandedIds] = useState<Record<number, boolean>>({});

  const toggleExpand = (id: number) => {
    setExpandedIds((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  if (isPending) {
    return (
      <div className="space-y-2 rounded-md border border-border bg-surface p-4">
        <Skeleton className="h-7 w-full" />
        <Skeleton className="h-7 w-full" />
        <Skeleton className="h-7 w-full" />
      </div>
    );
  }

  if (entries.length === 0) {
    return (
      <div className="rounded-md border border-border bg-surface px-4 py-8 text-center text-[13px] text-muted-foreground">
        {emptyMessage}
      </div>
    );
  }

  const colCount = showProject ? 7 : 6;

  return (
    <div className="space-y-3">
      <div className="overflow-x-auto rounded-md border border-border bg-surface">
        <table className="w-full border-collapse text-left text-[13px]">
          <thead>
            <tr className="border-b border-border bg-muted/40 text-xs font-medium text-muted-foreground">
              <th className="w-8 px-2 py-2" aria-label="Details" />
              <th className="px-3 py-2">Time</th>
              <th className="px-3 py-2">Actor</th>
              <th className="px-3 py-2">Action</th>
              {showProject ? <th className="px-3 py-2">Project</th> : null}
              <th className="px-3 py-2">Target</th>
              <th className="px-3 py-2">IP</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {entries.map((entry) => {
              const hasMetadata = entry.metadata !== null && Object.keys(entry.metadata).length > 0;
              const expanded = Boolean(expandedIds[entry.id]);
              const exact = formatExactTimestamp(entry.at);
              const targetLabel =
                entry.target_type && entry.target_id
                  ? `${entry.target_type}:${entry.target_id}`
                  : (entry.target_type ?? entry.target_id ?? "—");

              return (
                <Fragment key={entry.id}>
                  <tr className="hover:bg-muted/30">
                    <td className="px-2 py-2 align-middle">
                      {hasMetadata ? (
                        <button
                          type="button"
                          onClick={() => toggleExpand(entry.id)}
                          aria-expanded={expanded}
                          aria-label={expanded ? "Hide metadata" : "Show metadata"}
                          className="inline-flex size-6 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                        >
                          {expanded ? (
                            <ChevronDown className="size-3.5" aria-hidden="true" />
                          ) : (
                            <ChevronRight className="size-3.5" aria-hidden="true" />
                          )}
                        </button>
                      ) : null}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 font-mono text-xs tabular-nums text-muted-foreground">
                      <time dateTime={entry.at} title={exact.combined}>
                        {formatRelativeTime(entry.at)}
                      </time>
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex items-center gap-1.5">
                        <Badge variant="outline" className="text-[11px]">
                          {entry.actor_type}
                        </Badge>
                        <span className="truncate text-foreground">{entry.actor_label || "—"}</span>
                      </div>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2">
                      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                        {entry.action}
                      </code>
                    </td>
                    {showProject ? (
                      <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-muted-foreground">
                        {entry.project_slug ?? (entry.project_id ? `#${entry.project_id}` : "—")}
                      </td>
                    ) : null}
                    <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-muted-foreground">
                      {targetLabel}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 font-mono text-xs tabular-nums text-muted-foreground">
                      {entry.ip || "—"}
                    </td>
                  </tr>
                  {expanded && hasMetadata ? (
                    <tr className="bg-muted/20">
                      <td colSpan={colCount} className="px-4 py-3">
                        <div className="space-y-1">
                          <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                            Metadata
                          </div>
                          <pre
                            // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable JSON region needs keyboard focus (WCAG 2.1.1).
                            tabIndex={0}
                            className="overflow-x-auto rounded-md border border-border bg-background p-2.5 font-mono text-xs text-foreground"
                          >
                            {JSON.stringify(entry.metadata, null, 2)}
                          </pre>
                        </div>
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>

      {hasNextPage ? (
        <div className="flex justify-center">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={isFetchingNextPage}
            onClick={onLoadMore}
          >
            {isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
