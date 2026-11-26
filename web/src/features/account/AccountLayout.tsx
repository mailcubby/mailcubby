import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, KeyRound, Shield, SlidersHorizontal, User } from "lucide-react";
import type { ReactNode } from "react";
import {
  SettingsShell,
  settingsBackLinkClassName,
  settingsNavClassName,
  settingsNavIconClassName,
  settingsNavItemClassName,
} from "@/components/SettingsShell";
import { metaQueryOptions } from "@/lib/queries";
import { useSingleProjectSlug } from "@/lib/useSingleProjectSlug";

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
  const singleProjectSlug = useSingleProjectSlug();

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
    <SettingsShell
      back={
        // Audit V1: a user whose projects home opens their only project goes
        // straight back to that inbox.
        singleProjectSlug ? (
          <Link
            to="/p/$project"
            params={{ project: singleProjectSlug }}
            className={settingsBackLinkClassName}
          >
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to inbox</span>
          </Link>
        ) : (
          <Link to="/" className={settingsBackLinkClassName}>
            <ArrowLeft className="size-3.5" aria-hidden="true" />
            <span>Back to projects</span>
          </Link>
        )
      }
      title={title}
      description={description}
      nav={
        isServer ? (
          <nav aria-label="Account sections" className={settingsNavClassName}>
            {navItems.map(({ id, label, to, Icon }) => {
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
        ) : undefined
      }
    >
      {children}
    </SettingsShell>
  );
}
