export interface MessageListQueryParams {
  query?: string;
  tag?: string;
  credential?: number;
}

export interface AdminUsersQueryParams {
  q?: string;
}

export const queryKeys = {
  meta: () => ["meta"] as const,
  authMe: () => ["auth", "me"] as const,
  authSessions: () => ["auth", "sessions"] as const,
  projects: () => ["projects"] as const,
  project: (slug: string) => ["projects", slug] as const,
  projectMembers: (slug: string) => ["projects", slug, "members"] as const,
  projectInvites: (slug: string) => ["projects", slug, "invites"] as const,
  smtpCredentials: (slug: string) => ["projects", slug, "smtp-credentials"] as const,
  invitePreview: (token: string) => ["invites", token] as const,
  tags: (slug: string) => ["projects", slug, "tags"] as const,
  messages: (slug: string, params?: MessageListQueryParams) =>
    ["projects", slug, "messages", params ?? {}] as const,
  message: (slug: string, id: string) => ["projects", slug, "messages", id] as const,
  messageRaw: (slug: string, id: string) => ["projects", slug, "messages", id, "raw"] as const,
  messageHtml: (slug: string, id: string) => ["projects", slug, "messages", id, "html"] as const,
  messageHeaders: (slug: string, id: string) =>
    ["projects", slug, "messages", id, "headers"] as const,
  adminUsers: (params?: AdminUsersQueryParams) => ["admin", "users", params ?? {}] as const,
  adminProjects: () => ["admin", "projects"] as const,
} as const;
