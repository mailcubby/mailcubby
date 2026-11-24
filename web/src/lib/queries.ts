import { queryOptions } from "@tanstack/react-query";
import { ApiError, api, setCsrfToken, unwrap } from "@/lib/api";
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
    queryFn: async () => {
      const data = unwrap(await api.GET("/api/v1/auth/me"));
      setCsrfToken(data.csrf_token);
      return data;
    },
    staleTime: 60_000,
    retry: false,
  });
}

export function authSessionsQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.authSessions(),
    queryFn: async () => unwrap(await api.GET("/api/v1/auth/sessions")),
    staleTime: 10_000,
  });
}

export function apiTokensQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.apiTokens(),
    queryFn: async () => unwrap(await api.GET("/api/v1/tokens")),
    staleTime: 10_000,
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

export function projectMembersQueryOptions(project: string) {
  return queryOptions({
    queryKey: queryKeys.projectMembers(project),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/members", {
          params: { path: { project } },
        }),
      ),
    staleTime: 10_000,
  });
}

export function projectInvitesQueryOptions(project: string) {
  return queryOptions({
    queryKey: queryKeys.projectInvites(project),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/invites", {
          params: { path: { project } },
        }),
      ),
    staleTime: 10_000,
  });
}

export function invitePreviewQueryOptions(token: string) {
  return queryOptions({
    queryKey: queryKeys.invitePreview(token),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/invites/{token}", {
          params: { path: { token } },
        }),
      ),
    staleTime: 30_000,
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

export function projectSMTPCredentialsQueryOptions(project: string) {
  return queryOptions({
    queryKey: queryKeys.smtpCredentials(project),
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/projects/{project}/smtp-credentials", {
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

export function adminProjectsQueryOptions() {
  return queryOptions({
    queryKey: queryKeys.adminProjects(),
    queryFn: async () => unwrap(await api.GET("/api/v1/admin/projects")),
    staleTime: 10_000,
  });
}
