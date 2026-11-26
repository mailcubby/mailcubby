import { Link, useRouterState } from "@tanstack/react-router";
import { AlertCircle, ArrowLeft, FolderX, SearchX } from "lucide-react";
import type { ReactNode } from "react";
import { EmptyState } from "@/components/EmptyState";
import { Button, buttonClassName } from "@/components/ui/button";
import { ApiError, getApiErrorMessage } from "@/lib/api";

/** FullPageState centres a page-level EmptyState (not found, no access) in the content area. */
export function FullPageState({ children }: { children: ReactNode }) {
  return (
    <main className="flex h-full items-center justify-center overflow-y-auto px-4 py-10">
      {children}
    </main>
  );
}

/** BackToProjectsLink is the outline "Back to projects" action of page-level states. */
export function BackToProjectsLink() {
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
 * other errors offer Retry.
 */
export function ProjectLoadError({ projectSlug, error, onRetry }: ProjectLoadErrorProps) {
  const isNotFound = error instanceof ApiError && error.status === 404;
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
            <BackToProjectsLink />
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
