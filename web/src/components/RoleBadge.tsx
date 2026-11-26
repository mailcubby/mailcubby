import { Crown, Eye, type LucideIcon, Pencil } from "lucide-react";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import type { ProjectRole } from "@/lib/api";

const ROLE_STYLE: Record<ProjectRole, { variant: BadgeVariant; Icon: LucideIcon }> = {
  owner: { variant: "accent", Icon: Crown },
  member: { variant: "info", Icon: Pencil },
  viewer: { variant: "neutral", Icon: Eye },
};

export interface RoleBadgeProps {
  role: ProjectRole;
  className?: string;
}

/** RoleBadge shows a project role as text with a lucide icon (owner crown, member pencil, viewer eye). */
export function RoleBadge({ role, className }: RoleBadgeProps) {
  const { variant, Icon } = ROLE_STYLE[role];
  return (
    <Badge variant={variant} className={className}>
      <Icon className="size-3 shrink-0" aria-hidden="true" />
      {role}
    </Badge>
  );
}
