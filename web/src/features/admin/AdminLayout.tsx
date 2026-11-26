import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Activity,
  ArrowLeft,
  FolderKanban,
  ScrollText,
  Settings,
  ShieldAlert,
  Users,
} from "lucide-react";
import { type ReactNode, useEffect } from "react";
import {
  SettingsShell,
  settingsBackLinkClassName,
  settingsNavClassName,
  settingsNavIconClassName,
  settingsNavItemClassName,
} from "@/components/SettingsShell";
import { buttonClassName } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";

export type AdminSection = "users" | "projects" | "settings" | "audit" | "stats";

export interface AdminLayoutProps {
  section: AdminSection;
  title: string;
  description: string;
  actions?: ReactNode;
  children: ReactNode;
}

const NAV_ITEMS: ReadonlyArray<{
  id: AdminSection;
  label: string;
  to: "/admin/users" | "/admin/projects" | "/admin/settings" | "/admin/audit" | "/admin/stats";
  Icon: typeof Users;
}> = [
  { id: "users", label: "Users", to: "/admin/users", Icon: Users },
  { id: "projects", label: "Projects", to: "/admin/projects", Icon: FolderKanban },
  { id: "settings", label: "Settings", to: "/admin/settings", Icon: Settings },
  { id: "audit", label: "Audit log", to: "/admin/audit", Icon: ScrollText },
  { id: "stats", label: "Stats", to: "/admin/stats", Icon: Activity },
];

export function AdminLayout({ section, title, description, actions, children }: AdminLayoutProps) {
  const { data: meta, isPending: metaPending } = useQuery(metaQueryOptions());
  const { data: me, isPending: mePending } = useQuery(authMeQueryOptions());

  const instanceName = meta?.instance_name || "Mailcubby";
  const isServer = meta?.mode === "server";
  const isAdmin = Boolean(isServer && me?.user.is_admin);

  useEffect(() => {
    document.title = `${title} · Admin · ${instanceName}`;
  }, [title, instanceName]);

  if (metaPending || mePending) {
    return (
      <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-7 w-48" />
          <Skeleton className="h-64 w-full" />
        </div>
      </main>
    );
  }

  if (!isAdmin) {
    return (
      <main className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
        <ShieldAlert className="size-8 text-muted-foreground" aria-hidden="true" />
        <div className="max-w-md space-y-1">
          <h1 className="text-base font-semibold text-foreground">Administrator access required</h1>
          <p className="text-[13px] text-muted-foreground">
            {isServer
              ? "You need instance administrator privileges to view this page."
              : "Administration pages are only available in server mode."}
          </p>
        </div>
        <Link to="/" className={buttonClassName()}>
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>Back to projects</span>
        </Link>
      </main>
    );
  }

  return (
    <SettingsShell
      width="wide"
      back={
        <Link to="/" className={settingsBackLinkClassName}>
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>Back to projects</span>
        </Link>
      }
      title={title}
      description={description}
      actions={actions}
      nav={
        <nav aria-label="Admin sections" className={settingsNavClassName}>
          {NAV_ITEMS.map(({ id, label, to, Icon }) => {
            const active = section === id;
            return (
              <Link
                key={id}
                to={to}
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
      {children}
    </SettingsShell>
  );
}
