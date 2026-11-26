import { useQueryClient } from "@tanstack/react-query";
import { Link, useRouterState } from "@tanstack/react-router";
import { AlertCircle, ArrowLeft, FolderX, SearchX } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { EmptyState } from "@/components/EmptyState";
import { Button, buttonClassName } from "@/components/ui/button";
import { ApiError, getApiErrorMessage, type ProjectSummary } from "@/lib/api";
import { queryKeys } from "@/lib/queryKeys";
import { useSingleProjectSlug } from "@/lib/useSingleProjectSlug";

/** FullPageState centres a page-level EmptyState (not found, no access) in the content area. */
export function FullPageState({ children }: { children: ReactNode }) {
  return (
    <main className="flex h-full items-center justify-center overflow-y-auto px-4 py-10">
      {children}
    </main>
  );
}

/**
 * BackToProjectsLink is the outline "Back to projects" action of page-level
 * states. For a user whom the projects home sends to their only project (audit
 * V1) it reads "Back to inbox" and opens that inbox. excludeSlug names a
 * project that failed to load, which the link never points back at.
 */
export function BackToProjectsLink({ excludeSlug }: { excludeSlug?: string }) {
  const singleProjectSlug = useSingleProjectSlug(excludeSlug);
  if (singleProjectSlug) {
    return (
      <Link to="/p/$project" params={{ project: singleProjectSlug }} className={buttonClassName()}>
        <ArrowLeft className="size-3.5" aria-hidden="true" />
        <span>Back to inbox</span>
      </Link>
    );
  }
  return (
    <Link to="/" className={buttonClassName()}>
      <ArrowLeft className="size-3.5" aria-hidden="true" />
      <span>Back to projects</span>
    </Link>
  );
}

export interface ProjectLoadErrorProps {
  projectSlug: string;
  error: unknown;
  onRetry: () => void;
}

/**
 * ProjectLoadError is the inbox and settings state for a project that failed to
 * load: a 404 reads "Project not found or you do not have access" (UI.md 4.15),
 * other errors offer Retry. A 404 also drops the project from the cached
 * project list and refetches it, so the projects home does not redirect a
 * single-project user (audit V1) straight back here.
 */
export function ProjectLoadError({ projectSlug, error, onRetry }: ProjectLoadErrorProps) {
  const queryClient = useQueryClient();
  const isNotFound = error instanceof ApiError && error.status === 404;

  useEffect(() => {
    if (!isNotFound) {
      return;
    }
    queryClient.setQueryData<ProjectSummary[]>(queryKeys.projects(), (prev) =>
      prev?.filter((p) => p.slug !== projectSlug),
    );
    // exact: the failing project query is mounted here and must not refetch.
    void queryClient.invalidateQueries({ queryKey: queryKeys.projects(), exact: true });
  }, [isNotFound, projectSlug, queryClient]);

  return (
    <FullPageState>
      <EmptyState
        icon={isNotFound ? FolderX : AlertCircle}
        headingLevel={1}
        title={
          isNotFound ? "Project not found or you do not have access" : "Could not load project"
        }
        description={
          isNotFound
            ? `The project "${projectSlug}" does not exist or is unavailable.`
            : getApiErrorMessage(error)
        }
        action={
          <div className="flex flex-wrap items-center justify-center gap-2">
            <BackToProjectsLink excludeSlug={projectSlug} />
            {!isNotFound ? (
              <Button variant="primary" onClick={onRetry}>
                Retry
              </Button>
            ) : null}
          </div>
        }
      />
    </FullPageState>
  );
}

/** NotFoundPage is the router's default not-found screen for unknown addresses. */
export function NotFoundPage() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  return (
    <FullPageState>
      <EmptyState
        icon={SearchX}
        headingLevel={1}
        title="Page not found"
        description={
          <>
            Nothing lives at <code className="font-mono text-foreground">{pathname}</code>.
          </>
        }
        action={<BackToProjectsLink />}
      />
    </FullPageState>
  );
}
