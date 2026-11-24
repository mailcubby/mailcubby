import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, FolderKanban, ShieldAlert, Users } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export type AdminSection = "users" | "projects";

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
  to: "/admin/users" | "/admin/projects";
  Icon: typeof Users;
}> = [
  { id: "users", label: "Users", to: "/admin/users", Icon: Users },
  { id: "projects", label: "Projects", to: "/admin/projects", Icon: FolderKanban },
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
        <Link
          to="/"
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-[13px] font-medium text-foreground hover:bg-muted"
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>Back to projects</span>
        </Link>
      </main>
    );
  }

  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-6">
        <div className="flex flex-col gap-2">
          <Link
            to="/"
            className="inline-flex w-fit items-center gap-1.5 rounded-sm text-xs font-medium text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to projects</span>
          </Link>

          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h1 className="text-lg font-semibold tracking-tight text-foreground">{title}</h1>
              <p className="text-[13px] text-muted-foreground">{description}</p>
            </div>
            {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
          </div>
        </div>

        <div className="grid gap-6 md:grid-cols-[180px_minmax(0,1fr)]">
          <nav aria-label="Admin sections" className="flex flex-row gap-1 md:flex-col">
            {NAV_ITEMS.map(({ id, label, to, Icon }) => {
              const active = section === id;
              return (
                <Link
                  key={id}
                  to={to}
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

          <div className="min-w-0">{children}</div>
        </div>
      </div>
    </main>
  );
}
