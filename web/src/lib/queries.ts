import { queryOptions } from "@tanstack/react-query";
import { api, unwrap } from "@/lib/api";
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
