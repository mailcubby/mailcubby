import { queryOptions } from "@tanstack/react-query";
import { ApiError, api, unwrap } from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";

export function metaQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.meta(),
    queryFn: async () => unwrap(await api.GET("/api/v1/meta")),
    staleTime: 60_000,
  });
}

export function authMeQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.authMe(),
    queryFn: async () => unwrap(await api.GET("/api/v1/auth/me")),
    staleTime: 60_000,
  });
}

export function projectsQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.projects(),
    queryFn: async () => unwrap(await api.GET("/api/v1/projects")),
    staleTime: 10_000,
  });
}

export function projectQueryOptions(project: string) {
  return queryOptions({
    queryKey: queryKeys.project(project),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}", {
          params: { path: { project } },
        }),
      ),
    staleTime: 10_000,
    retry: false,
  });
}

export function projectTagsQueryOptions(project: string) {
  return queryOptions({
    queryKey: queryKeys.tags(project),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/tags", {
          params: { path: { project } },
        }),
      ),
    staleTime: 10_000,
  });
}

export function messageDetailQueryOptions(project: string, messageId: string) {
  return queryOptions({
    queryKey: queryKeys.message(project, messageId),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/messages/{id}", {
          params: { path: { project, id: messageId } },
        }),
      ),
    staleTime: 30_000,
    retry: (count, err) => {
      if (err instanceof ApiError && err.status === 404) {
        return false;
      }
      return count < 1;
    },
  });
}
