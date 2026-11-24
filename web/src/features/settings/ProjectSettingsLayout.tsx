import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  AlertCircle,
  ArrowLeft,
  Code2,
  KeyRound,
  Route as RouteIcon,
  Settings2,
  Users,
} from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, getApiErrorMessage, type ProjectDetail } from "@/lib/api";
import { PROJECT_COLOR_DOT_CLASS } from "@/lib/format";
import { metaQueryOptions, projectQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export type ProjectSettingsSection = "general" | "smtp" | "members" | "routing" | "api";

export interface ProjectSettingsLayoutProps {
  projectSlug: string;
  section: ProjectSettingsSection;
  title: string;
  description: string;
  children: (project: ProjectDetail) => ReactNode;
}

export function ProjectSettingsLayout({
  projectSlug,
  section,
  title,
  description,
  children,
}: ProjectSettingsLayoutProps) {
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: project, isPending, error, refetch } = useQuery(projectQueryOptions(projectSlug));

  const isServer = meta?.mode === "server";
  const instanceName = meta?.instance_name || "Mailcubby";

  useEffect(() => {
    if (project) {
      document.title = `${title} · ${project.name} · ${instanceName}`;
    } else {
      document.title = `${title} · ${instanceName}`;
    }
  }, [project, title, instanceName]);

  if (error) {
    const isNotFound = error instanceof ApiError && error.status === 404;
    return (
      <main className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
        <AlertCircle className="size-8 text-muted-foreground" aria-hidden="true" />
        <div className="max-w-md space-y-1">
          <h1 className="text-base font-semibold text-foreground">
            {isNotFound ? "Project not found or you do not have access" : "Could not load project"}
          </h1>
          <p className="text-[13px] text-muted-foreground">
            {isNotFound
              ? `The project "${projectSlug}" does not exist or is unavailable.`
              : getApiErrorMessage(error)}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/"
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-[13px] font-medium text-foreground hover:bg-muted"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to projects</span>
          </Link>
          {!isNotFound ? (
            <Button variant="default" size="sm" onClick={() => void refetch()}>
              Retry
            </Button>
          ) : null}
        </div>
      </main>
    );
  }

  if (isPending || !project) {
    return (
      <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
        <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
          <Skeleton className="h-5 w-36" />
          <Skeleton className="h-7 w-52" />
          <div className="grid gap-6 md:grid-cols-[190px_minmax(0,1fr)]">
            <div className="flex flex-col gap-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
            <Skeleton className="h-64 w-full" />
          </div>
        </div>
      </main>
    );
  }

  if (project.role === "viewer") {
    return (
      <main className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
        <AlertCircle className="size-8 text-muted-foreground" aria-hidden="true" />
        <div className="max-w-md space-y-1">
          <h1 className="text-base font-semibold text-foreground">Settings unavailable</h1>
          <p className="text-[13px] text-muted-foreground">
            Project settings are not available for viewers.
          </p>
        </div>
        <Link
          to="/p/$project"
          params={{ project: project.slug }}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-[13px] font-medium text-foreground hover:bg-muted"
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>Back to {project.name}</span>
        </Link>
      </main>
    );
  }

  const isOwner = project.role === "owner";
  const dotClass = PROJECT_COLOR_DOT_CLASS[project.color] ?? PROJECT_COLOR_DOT_CLASS.slate;

  const navItems: Array<{
    id: ProjectSettingsSection;
    label: string;
    to:
      | "/p/$project/settings/general"
      | "/p/$project/settings/smtp"
      | "/p/$project/settings/members"
      | "/p/$project/settings/routing"
      | "/p/$project/settings/api";
    Icon: typeof Settings2;
  }> = [];

  if (isOwner) {
    navItems.push({
      id: "general",
      label: "General",
      to: "/p/$project/settings/general",
      Icon: Settings2,
    });
  }
  navItems.push({
    id: "smtp",
    label: "SMTP",
    to: "/p/$project/settings/smtp",
    Icon: KeyRound,
  });
  if (isServer) {
    navItems.push({
      id: "members",
      label: "Members & invites",
      to: "/p/$project/settings/members",
      Icon: Users,
    });
  }
  if (isOwner) {
    navItems.push({
      id: "routing",
      label: "Routing & client view",
      to: "/p/$project/settings/routing",
      Icon: RouteIcon,
    });
  }
  navItems.push({
    id: "api",
    label: "API",
    to: "/p/$project/settings/api",
    Icon: Code2,
  });

  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
        <div className="flex flex-col gap-2">
          <Link
            to="/p/$project"
            params={{ project: project.slug }}
            className="inline-flex w-fit items-center gap-1.5 rounded-sm text-xs font-medium text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to {project.name}</span>
          </Link>

          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2">
                <span
                  className={cn("size-2.5 shrink-0 rounded-full", dotClass)}
                  aria-hidden="true"
                />
                <h1 className="text-lg font-semibold tracking-tight text-foreground">
                  {project.name}
                </h1>
                <Badge variant="outline">{project.role}</Badge>
              </div>
              <p className="mt-0.5 text-[13px] text-muted-foreground">{description}</p>
            </div>
          </div>
        </div>

        <div className="grid gap-6 md:grid-cols-[190px_minmax(0,1fr)]">
          <nav aria-label="Project settings sections" className="flex flex-row gap-1 md:flex-col">
            {navItems.map(({ id, label, to, Icon }) => {
              const active = section === id;
              return (
                <Link
                  key={id}
                  to={to}
                  params={{ project: project.slug }}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-[13px] font-medium transition-colors",
                    active
                      ? "bg-surface text-foreground shadow-2xs ring-1 ring-border"
                      : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                  )}
                >
                  <Icon className="size-3.5 shrink-0" aria-hidden="true" />
                  <span>{label}</span>
                </Link>
              );
            })}
          </nav>

          <div className="min-w-0">{children(project)}</div>
        </div>
      </div>
    </main>
  );
}
