import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useState } from "react";
import { ALL_AUDIT_ACTIONS, AuditLogTable } from "@/components/AuditLogTable";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { AdminLayout } from "@/features/admin/AdminLayout";
import { api, getApiErrorMessage, unwrap } from "@/lib/api";
import { adminProjectsQueryOptions } from "@/lib/queries";
import { type AdminAuditQueryParams, queryKeys } from "@/lib/queryKeys";

export function AdminAuditPage() {
  const [actionFilter, setActionFilter] = useState("");
  const [projectFilter, setProjectFilter] = useState("");
  const [userFilter, setUserFilter] = useState("");

  const { data: projects } = useQuery(adminProjectsQueryOptions());

  const trimmedUser = userFilter.trim();
  const filterParams: AdminAuditQueryParams = {};
  if (actionFilter) {
    filterParams.action = actionFilter;
  }
  if (projectFilter) {
    filterParams.project = projectFilter;
  }
  if (trimmedUser) {
    filterParams.user = trimmedUser;
  }

  const auditQuery = useInfiniteQuery({
    queryKey: queryKeys.adminAudit(filterParams),
    queryFn: async ({ pageParam }) => {
      const q: {
        cursor?: string;
        limit: number;
        action?: string;
        project?: string;
        user?: string;
      } = { limit: 50 };
      if (pageParam) {
        q.cursor = pageParam;
      }
      if (actionFilter) {
        q.action = actionFilter;
      }
      if (projectFilter) {
        q.project = projectFilter;
      }
      if (trimmedUser) {
        q.user = trimmedUser;
      }
      return unwrap(
        await api.GET("/api/v1/admin/audit", {
          params: { query: q },
        }),
      );
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });

  const entries = auditQuery.data?.pages.flatMap((p) => p.items) ?? [];
  const hasActiveFilters = Boolean(actionFilter || projectFilter || trimmedUser);

  const clearFilters = () => {
    setActionFilter("");
    setProjectFilter("");
    setUserFilter("");
  };

  return (
    <AdminLayout
      section="audit"
      title="Audit log"
      description="Security, authentication, and administration events across the instance."
      actions={
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
      }
    >
      <div className="space-y-4">
        <div className="flex flex-wrap items-end gap-3 rounded-lg border border-border bg-surface px-5 py-4 shadow-xs">
          <div className="space-y-1">
            <label
              htmlFor="admin-audit-action"
              className="block text-xs font-medium text-muted-foreground"
            >
              Action
            </label>
            <NativeSelect
              id="admin-audit-action"
              value={actionFilter}
              onChange={(e) => setActionFilter(e.target.value)}
            >
              <option value="">All actions</option>
              {ALL_AUDIT_ACTIONS.map((action) => (
                <option key={action} value={action}>
                  {action}
                </option>
              ))}
            </NativeSelect>
          </div>

          <div className="space-y-1">
            <label
              htmlFor="admin-audit-project"
              className="block text-xs font-medium text-muted-foreground"
            >
              Project
            </label>
            <NativeSelect
              id="admin-audit-project"
              value={projectFilter}
              onChange={(e) => setProjectFilter(e.target.value)}
            >
              <option value="">All projects</option>
              {(projects ?? []).map((p) => (
                <option key={p.id} value={p.slug}>
                  {p.name} ({p.slug})
                </option>
              ))}
            </NativeSelect>
          </div>

          <div className="min-w-48 flex-1 space-y-1">
            <label
              htmlFor="admin-audit-user"
              className="block text-xs font-medium text-muted-foreground"
            >
              Actor (email, label, or user ID)
            </label>
            <Input
              id="admin-audit-user"
              type="search"
              value={userFilter}
              onChange={(e) => setUserFilter(e.target.value)}
              placeholder="admin@example.com or 1"
              className="h-8"
            />
          </div>

          {hasActiveFilters ? (
            <Button type="button" variant="ghost" size="sm" onClick={clearFilters}>
              Clear filters
            </Button>
          ) : null}
        </div>

        {auditQuery.error ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive-subtle px-5 py-3 text-[13px] text-destructive">
            {getApiErrorMessage(auditQuery.error, "Failed to load audit log.")}
          </div>
        ) : (
          <AuditLogTable
            entries={entries}
            isPending={auditQuery.isPending}
            showProject
            hasNextPage={auditQuery.hasNextPage}
            isFetchingNextPage={auditQuery.isFetchingNextPage}
            onLoadMore={() => void auditQuery.fetchNextPage()}
          />
        )}
      </div>
    </AdminLayout>
  );
}
