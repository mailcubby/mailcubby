import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Archive,
  ArrowLeft,
  Code2,
  KeyRound,
  Lock,
  Route as RouteIcon,
  ScrollText,
  Settings2,
  Users,
} from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { EmptyState } from "@/components/EmptyState";
import { FullPageState, ProjectLoadError } from "@/components/PageStates";
import { RoleBadge } from "@/components/RoleBadge";
import {
  SettingsShell,
  settingsBackLinkClassName,
  settingsNavClassName,
  settingsNavIconClassName,
  settingsNavItemClassName,
} from "@/components/SettingsShell";
import { buttonClassName } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { ProjectDetail } from "@/lib/api";
import { PROJECT_COLOR_DOT_CLASS } from "@/lib/format";
import { metaQueryOptions, projectQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export type ProjectSettingsSection =
  | "general"
  | "smtp"
  | "members"
  | "retention"
  | "routing"
  | "api"
  | "audit";

export interface ProjectSettingsLayoutProps {
  projectSlug: string;
  section: ProjectSettingsSection;
  title: string;
  /** Page subtitle; a function when the text depends on the project or role (audit M5). */
  description: string | ((project: ProjectDetail) => string);
  /** The page's main action in the header (audit M7), rendered once the project loads. */
  actions?: (project: ProjectDetail) => ReactNode;
  children: (project: ProjectDetail) => ReactNode;
}

export function ProjectSettingsLayout({
  projectSlug,
  section,
  title,
  description,
  actions,
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
    return (
      <ProjectLoadError projectSlug={projectSlug} error={error} onRetry={() => void refetch()} />
    );
  }

  if (isPending || !project) {
    return (
      <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-6">
          <Skeleton className="h-5 w-36" />
          <Skeleton className="h-7 w-52" />
          <div className="grid gap-6 md:grid-cols-[200px_minmax(0,1fr)] lg:gap-8">
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
      <FullPageState>
        <EmptyState
          icon={Lock}
          headingLevel={1}
          title="Settings unavailable"
          description="Project settings are not available for viewers."
          action={
            <Link to="/p/$project" params={{ project: project.slug }} className={buttonClassName()}>
              <ArrowLeft className="size-3.5" aria-hidden="true" />
              <span>Back to {project.name}</span>
            </Link>
          }
        />
      </FullPageState>
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
      | "/p/$project/settings/retention"
      | "/p/$project/settings/routing"
      | "/p/$project/settings/api"
      | "/p/$project/settings/audit";
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
      id: "retention",
      label: "Retention",
      to: "/p/$project/settings/retention",
      Icon: Archive,
    });
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
  if (isServer && isOwner) {
    navItems.push({
      id: "audit",
      label: "Audit log",
      to: "/p/$project/settings/audit",
      Icon: ScrollText,
    });
  }

  return (
    <SettingsShell
      back={
        <Link
          to="/p/$project"
          params={{ project: project.slug }}
          className={settingsBackLinkClassName}
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>Back to {project.name}</span>
        </Link>
      }
      leading={
        <span className={cn("size-2.5 shrink-0 rounded-full", dotClass)} aria-hidden="true" />
      }
      title={project.name}
      trailing={<RoleBadge role={project.role} />}
      description={typeof description === "function" ? description(project) : description}
      actions={actions?.(project)}
      nav={
        <nav aria-label="Project settings sections" className={settingsNavClassName}>
          {navItems.map(({ id, label, to, Icon }) => {
            const active = section === id;
            return (
              <Link
                key={id}
                to={to}
                params={{ project: project.slug }}
                aria-current={active ? "page" : undefined}
                className={settingsNavItemClassName(active)}
              >
                <Icon className={settingsNavIconClassName(active)} aria-hidden="true" />
                <span>{label}</span>
              </Link>
            );
          })}
        </nav>
      }
    >
      {children(project)}
    </SettingsShell>
  );
}
