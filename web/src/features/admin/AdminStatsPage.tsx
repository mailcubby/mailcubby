import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Clock,
  Database,
  FolderKanban,
  HardDrive,
  Mail,
  RefreshCw,
  Server,
  Users,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { AdminLayout } from "@/features/admin/AdminLayout";
import { getApiErrorMessage } from "@/lib/api";
import {
  formatBytes,
  formatCount,
  formatExactTimestamp,
  formatRelativeTime,
  formatUptime,
} from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { adminStatsQueryOptions, authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export function AdminStatsPage() {
  const [clock24h] = useClock24hPreference();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());
  const isAdmin = Boolean(meta?.mode === "server" && me?.user.is_admin);

  const statsQuery = useQuery({
    ...adminStatsQueryOptions(),
    enabled: isAdmin,
  });

  const stats = statsQuery.data;
  const now = new Date();

  const quotaPct =
    stats && stats.instance_max_bytes > 0
      ? Math.min(100, Math.round((stats.stored_bytes / stats.instance_max_bytes) * 100))
      : null;

  const compressionSavingsPct =
    stats && stats.stored_bytes > 0 && stats.blob_size_bytes > 0
      ? Math.max(0, Math.round((1 - stats.blob_size_bytes / stats.stored_bytes) * 100))
      : null;

  return (
    <AdminLayout
      section="stats"
      title="Instance statistics"
      description="Live storage footprint, ingest throughput, and runtime health across this instance."
      actions={
        <Button
          variant="outline"
          size="sm"
          disabled={statsQuery.isFetching}
          onClick={() => void statsQuery.refetch()}
        >
          <RefreshCw
            className={cn("size-3.5", statsQuery.isFetching && "animate-spin")}
            aria-hidden="true"
          />
          <span>Refresh</span>
        </Button>
      }
    >
      {statsQuery.isPending ? (
        <div className="flex flex-col gap-4">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
          <Skeleton className="h-44 w-full" />
        </div>
      ) : statsQuery.error ? (
        <div
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive-subtle p-5 text-[13px] text-destructive"
        >
          Failed to load instance statistics: {getApiErrorMessage(statsQuery.error)}
        </div>
      ) : stats ? (
        <div className="flex flex-col gap-6">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div className="rounded-lg border border-border bg-surface p-4 shadow-xs">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>Projects</span>
                <FolderKanban className="size-3.5" aria-hidden="true" />
              </div>
              <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                {formatCount(stats.projects)}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-surface p-4 shadow-xs">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>Users</span>
                <Users className="size-3.5" aria-hidden="true" />
              </div>
              <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                {formatCount(stats.users)}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-surface p-4 shadow-xs">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>Messages</span>
                <Mail className="size-3.5" aria-hidden="true" />
              </div>
              <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                {formatCount(stats.messages)}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-surface p-4 shadow-xs">
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span>Ingest (last hour)</span>
                <Activity className="size-3.5" aria-hidden="true" />
              </div>
              <div className="mt-2 text-2xl font-semibold tracking-tight text-foreground tabular-nums">
                {formatCount(stats.ingest_last_hour)}
              </div>
              <div className="mt-0.5 text-[11px] text-muted-foreground tabular-nums">
                {stats.ingest_rate_per_minute.toFixed(2)} msg/min
              </div>
            </div>
          </div>

          <Card className="p-5">
            <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">Storage</h2>
            <p className="mt-0.5 text-[13px] text-muted-foreground">
              Raw message volume vs compressed blob storage and SQLite metadata database size.
            </p>

            <div className="mt-4 grid gap-4 sm:grid-cols-3">
              <div className="rounded-md border border-border bg-background/60 p-3.5">
                <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <HardDrive className="size-3.5" aria-hidden="true" />
                  <span>Stored message bytes</span>
                </div>
                <div className="mt-1.5 text-lg font-semibold text-foreground tabular-nums">
                  {formatBytes(stats.stored_bytes)}
                </div>
                <div className="mt-0.5 text-[11px] text-muted-foreground tabular-nums">
                  {stats.instance_max_bytes > 0
                    ? `of ${formatBytes(stats.instance_max_bytes)} instance quota`
                    : "No instance quota configured"}
                </div>
                {quotaPct !== null ? (
                  <div className="mt-2.5">
                    <div
                      role="progressbar"
                      aria-label="Instance storage quota usage"
                      aria-valuemin={0}
                      aria-valuemax={100}
                      aria-valuenow={quotaPct}
                      className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
                    >
                      <div
                        className={cn(
                          "h-full rounded-full transition-all",
                          quotaPct >= 100
                            ? "bg-destructive"
                            : quotaPct >= 80
                              ? "bg-warning"
                              : "bg-accent",
                        )}
                        style={{ width: `${quotaPct}%` }}
                      />
                    </div>
                  </div>
                ) : null}
              </div>

              <div className="rounded-md border border-border bg-background/60 p-3.5">
                <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <HardDrive className="size-3.5" aria-hidden="true" />
                  <span>Blob files on disk</span>
                </div>
                <div className="mt-1.5 text-lg font-semibold text-foreground tabular-nums">
                  {formatBytes(stats.blob_size_bytes)}
                </div>
                <div className="mt-0.5 text-[11px] text-muted-foreground tabular-nums">
                  {compressionSavingsPct !== null
                    ? `zstd compressed (~${compressionSavingsPct}% saved)`
                    : "zstd compressed raw .eml blobs"}
                </div>
              </div>

              <div className="rounded-md border border-border bg-background/60 p-3.5">
                <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <Database className="size-3.5" aria-hidden="true" />
                  <span>SQLite database</span>
                </div>
                <div className="mt-1.5 text-lg font-semibold text-foreground tabular-nums">
                  {formatBytes(stats.db_size_bytes)}
                </div>
                <div className="mt-0.5 text-[11px] text-muted-foreground">
                  Includes metadata, FTS5 index, and WAL
                </div>
              </div>
            </div>
          </Card>

          <Card className="p-5">
            <h2 className="text-[15px] leading-[22px] font-semibold text-foreground">Runtime</h2>
            <dl className="mt-4 grid gap-4 sm:grid-cols-3">
              <div>
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <Server className="size-3.5" aria-hidden="true" />
                  <span>Version</span>
                </dt>
                <dd className="mt-1 font-mono text-sm font-medium text-foreground">
                  {stats.version}
                </dd>
              </div>
              <div>
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <Clock className="size-3.5" aria-hidden="true" />
                  <span>Uptime</span>
                </dt>
                <dd className="mt-1 text-sm font-medium text-foreground tabular-nums">
                  {formatUptime(stats.uptime_seconds)}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Started</dt>
                <dd
                  className="mt-1 text-sm font-medium text-foreground tabular-nums"
                  title={formatExactTimestamp(stats.started_at, clock24h).combined}
                >
                  {formatRelativeTime(stats.started_at, now, clock24h)}
                </dd>
              </div>
            </dl>
          </Card>
        </div>
      ) : null}
    </AdminLayout>
  );
}
