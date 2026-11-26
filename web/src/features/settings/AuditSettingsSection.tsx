import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { AlertCircle, RefreshCw } from "lucide-react";
import { useState } from "react";
import { AuditLogTable, PROJECT_AUDIT_ACTIONS } from "@/components/AuditLogTable";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/select";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import { api, getApiErrorMessage, type ProjectDetail, unwrap } from "@/lib/api";
import { metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

export interface AuditSettingsSectionProps {
  projectSlug: string;
}

export function AuditSettingsSection({ projectSlug }: AuditSettingsSectionProps) {
  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="audit"
      title="Audit log"
      description="Security and management events recorded for this project."
    >
      {(project) => <AuditSettingsContent project={project} />}
    </ProjectSettingsLayout>
  );
}

function AuditSettingsContent({ project }: { project: ProjectDetail }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";
  const isOwner = project.role === "owner";

  const [actionFilter, setActionFilter] = useState("");

  const queryParams = actionFilter ? { action: actionFilter } : undefined;
  const auditQuery = useInfiniteQuery({
    queryKey: queryKeys.projectAudit(project.slug, queryParams),
    queryFn: async ({ pageParam }) => {
      const q: { cursor?: string; limit: number; action?: string } = { limit: 50 };
      if (pageParam) {
        q.cursor = pageParam;
      }
      if (actionFilter) {
        q.action = actionFilter;
      }
      return unwrap(
        await api.GET("/api/v1/projects/{project}/audit", {
          params: { path: { project: project.slug }, query: q },
        }),
      );
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    enabled: isServer && isOwner,
  });

  if (!isServer || !isOwner) {
    return (
      <div className="rounded-md border border-border bg-surface p-6 text-center">
        <AlertCircle className="mx-auto mb-2 size-6 text-muted-foreground" aria-hidden="true" />
        <p className="text-[13px] font-medium text-foreground">Audit log unavailable</p>
        <p className="mt-1 text-xs text-muted-foreground">
          {isServer
            ? "Only project owners and instance administrators can view the project audit log."
            : "Audit logs are available in server mode."}
        </p>
      </div>
    );
  }

  const entries = auditQuery.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3 rounded-lg border border-border bg-surface px-5 py-4 shadow-xs">
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <label
              htmlFor="project-audit-action"
              className="block text-xs font-medium text-muted-foreground"
            >
              Action
            </label>
            <NativeSelect
              id="project-audit-action"
              value={actionFilter}
              onChange={(e) => setActionFilter(e.target.value)}
            >
              <option value="">All actions</option>
              {PROJECT_AUDIT_ACTIONS.map((action) => (
                <option key={action} value={action}>
                  {action}
                </option>
              ))}
            </NativeSelect>
          </div>

          {actionFilter ? (
            <Button type="button" variant="ghost" size="sm" onClick={() => setActionFilter("")}>
              Clear filter
            </Button>
          ) : null}
        </div>

        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={auditQuery.isFetching}
          onClick={() => void auditQuery.refetch()}
        >
          <RefreshCw className="size-3.5" aria-hidden="true" />
          <span>Refresh</span>
        </Button>
      </div>

      {auditQuery.error ? (
        <div
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive-subtle px-5 py-3 text-[13px] text-destructive"
        >
          {getApiErrorMessage(auditQuery.error, "Failed to load project audit log.")}
        </div>
      ) : (
        <AuditLogTable
          entries={entries}
          isPending={auditQuery.isPending}
          hasNextPage={auditQuery.hasNextPage}
          isFetchingNextPage={auditQuery.isFetchingNextPage}
          onLoadMore={() => void auditQuery.fetchNextPage()}
        />
      )}
    </section>
  );
}
