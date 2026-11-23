export interface MessageListQueryParams {
  query?: string;
  tag?: string;
  credential?: number;
}

export const queryKeys = {
  meta: () => ["meta"] as const,
  authMe: () => ["auth", "me"] as const,
  projects: () => ["projects"] as const,
  project: (slug: string) => ["projects", slug] as const,
  messages: (slug: string, params?: MessageListQueryParams) =>
    ["projects", slug, "messages", params ?? {}] as const,
  message: (slug: string, id: string) => ["projects", slug, "messages", id] as const,
  messageRaw: (slug: string, id: string) => ["projects", slug, "messages", id, "raw"] as const,
  messageHtml: (slug: string, id: string) => ["projects", slug, "messages", id, "html"] as const,
  messageHeaders: (slug: string, id: string) =>
    ["projects", slug, "messages", id, "headers"] as const,
} as const;
