import { useQuery } from "@tanstack/react-query";
import { authMeQueryOptions, metaQueryOptions, projectsQueryOptions } from "@/lib/queries";
import { getSingleProjectSlug } from "@/lib/viewerPreview";

/**
 * useSingleProjectSlug returns the slug of the project the projects home
 * redirects to for this user (audit V1, SPEC FR-035), or null when the user
 * sees the projects home. excludeSlug leaves out a project that just failed to
 * load, so links never point back at it.
 */
export function useSingleProjectSlug(excludeSlug?: string): string | null {
  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";
  const { data: me } = useQuery({ ...authMeQueryOptions(), enabled: isServer });
  const redirectPossible =
    isServer && me !== undefined && !me.user.is_admin && !me.user.can_create_projects;
  const { data: projects } = useQuery({ ...projectsQueryOptions(), enabled: redirectPossible });
  const candidates =
    excludeSlug === undefined ? projects : projects?.filter((p) => p.slug !== excludeSlug);
  return getSingleProjectSlug(meta?.mode, me?.user, candidates);
}
