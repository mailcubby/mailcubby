import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, KeyRound, Shield, SlidersHorizontal, User } from "lucide-react";
import type { ReactNode } from "react";
import { metaQueryOptions } from "@/lib/queries";
import { cn } from "@/lib/utils";

export type AccountSection = "profile" | "security" | "tokens" | "preferences";

export interface AccountLayoutProps {
  section: AccountSection;
  title: string;
  description: string;
  children: ReactNode;
}

export function AccountLayout({ section, title, description, children }: AccountLayoutProps) {
  const { data: meta } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";

  const navItems: ReadonlyArray<{
    id: AccountSection;
    label: string;
    to: "/account/profile" | "/account/security" | "/account/tokens" | "/account/preferences";
    Icon: typeof User;
  }> = isServer
    ? [
        { id: "profile", label: "Profile", to: "/account/profile", Icon: User },
        { id: "security", label: "Security", to: "/account/security", Icon: Shield },
        { id: "tokens", label: "API tokens", to: "/account/tokens", Icon: KeyRound },
        {
          id: "preferences",
          label: "Preferences",
          to: "/account/preferences",
          Icon: SlidersHorizontal,
        },
      ]
    : [
        {
          id: "preferences",
          label: "Preferences",
          to: "/account/preferences",
          Icon: SlidersHorizontal,
        },
      ];

  return (
    <main className="h-full overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
        <div className="flex flex-col gap-2">
          <Link
            to="/"
            className="inline-flex w-fit items-center gap-1.5 rounded-sm text-xs font-medium text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to projects</span>
          </Link>
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-foreground">{title}</h1>
            <p className="text-[13px] text-muted-foreground">{description}</p>
          </div>
        </div>

        <div className={cn("grid gap-6", isServer ? "md:grid-cols-[180px_minmax(0,1fr)]" : "")}>
          {isServer ? (
            <nav aria-label="Account sections" className="flex flex-row gap-1 md:flex-col">
              {navItems.map(({ id, label, to, Icon }) => {
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
          ) : null}

          <div className="min-w-0">{children}</div>
        </div>
      </div>
    </main>
  );
}
