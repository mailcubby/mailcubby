import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { CheckCircle2, Copy, Eye, LogOut, MailPlus, Trash2, UserMinus } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { useAppCommands } from "@/components/AppCommandContext";
import { RoleBadge } from "@/components/RoleBadge";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ProjectSettingsLayout } from "@/features/settings/ProjectSettingsLayout";
import {
  ApiError,
  api,
  type CreateProjectInviteResponse,
  getApiErrorMessage,
  handleApiError,
  type InviteStatus,
  type ProjectDetail,
  type ProjectInvite,
  type ProjectMember,
  type ProjectRole,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatExactTimestamp, formatRelativeTime } from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import {
  authMeQueryOptions,
  metaQueryOptions,
  projectInvitesQueryOptions,
  projectMembersQueryOptions,
} from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

const ROLE_OPTIONS: ReadonlyArray<{ value: ProjectRole; label: string; description: string }> = [
  {
    value: "viewer",
    label: "Viewer",
    description: "Read-only access to messages; cannot delete, tag, or view SMTP credentials.",
  },
  {
    value: "member",
    label: "Member",
    description: "Read, tag, mark read/unread, delete messages, and view SMTP connection info.",
  },
  {
    value: "owner",
    label: "Owner",
    description: "Full project control including settings, members, invites, and deletion.",
  },
];

function inviteStatusBadgeVariant(status: InviteStatus): BadgeVariant {
  switch (status) {
    case "pending":
      return "warning";
    case "accepted":
      return "success";
    case "revoked":
    case "expired":
      return "neutral";
  }
}

interface MembersSettingsContentProps {
  project: ProjectDetail;
  /** The invite dialog is opened from the page header ("Invite people", audit M7). */
  inviteOpen: boolean;
  onInviteOpenChange: (open: boolean) => void;
}

function MembersSettingsContent({
  project,
  inviteOpen,
  onInviteOpenChange,
}: MembersSettingsContentProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { setPreviewAsViewer } = useAppCommands();
  const [clock24h] = useClock24hPreference();

  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());

  const isServer = meta?.mode === "server";
  const isOwner = project.role === "owner";

  useEffect(() => {
    if (meta && meta.mode !== "server") {
      void navigate({
        to: "/p/$project/settings/general",
        params: { project: project.slug },
        replace: true,
      });
    }
  }, [meta, navigate, project.slug]);

  const membersQuery = useQuery({
    ...projectMembersQueryOptions(project.slug),
    enabled: isServer,
  });

  const invitesQuery = useQuery({
    ...projectInvitesQueryOptions(project.slug),
    enabled: isServer && isOwner,
  });

  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<ProjectRole>("viewer");
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [createdInvite, setCreatedInvite] = useState<CreateProjectInviteResponse | null>(null);

  const [removingMember, setRemovingMember] = useState<ProjectMember | null>(null);
  const [revokingInvite, setRevokingInvite] = useState<ProjectInvite | null>(null);
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [showAccepted, setShowAccepted] = useState(false);
  const [pendingRoleChange, setPendingRoleChange] = useState<{
    member: ProjectMember;
    role: ProjectRole;
  } | null>(null);

  const emailId = useId();
  const roleId = useId();
  const showAcceptedId = useId();

  const updateRoleMutation = useMutation({
    mutationFn: async ({ userId, role }: { userId: number; role: ProjectRole }) =>
      unwrap(
        await api.PATCH("/api/v1/projects/{project}/members/{user_id}", {
          params: { path: { project: project.slug, user_id: userId } },
          body: { role },
        }),
      ),
    onSuccess: async (updated) => {
      setPendingRoleChange(null);
      toast.success(`Updated ${updated.user.name} to ${updated.role}`);
      const isSelfViewerDemotion =
        updated.user.id === me?.user.id && updated.role === "viewer" && !me?.user.is_admin;
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projectMembers(project.slug) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.project(project.slug) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
      if (isSelfViewerDemotion) {
        await navigate({ to: "/p/$project", params: { project: project.slug }, replace: true });
      }
    },
    onError: (err) => {
      setPendingRoleChange(null);
      handleApiError(err, "Failed to update role");
    },
  });

  const removeMemberMutation = useMutation({
    mutationFn: async (member: ProjectMember) =>
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}/members/{user_id}", {
          params: { path: { project: project.slug, user_id: member.user.id } },
        }),
      ),
    onSuccess: async (_data, member) => {
      setRemovingMember(null);
      const removedSelfWithoutAccess = member.user.id === me?.user.id && !me?.user.is_admin;
      if (removedSelfWithoutAccess) {
        queryClient.removeQueries({ queryKey: queryKeys.project(project.slug) });
        await Promise.all([
          queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
          queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
        ]);
        toast.success(`Removed yourself from ${project.name}`);
        await navigate({ to: "/", replace: true });
        return;
      }
      toast.success(`Removed ${member.user.name} from ${project.name}`);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projectMembers(project.slug) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
    },
    onError: (err) => {
      setRemovingMember(null);
      handleApiError(err, "Failed to remove member");
    },
  });

  const leaveMutation = useMutation({
    mutationFn: async () =>
      unwrapVoid(
        await api.POST("/api/v1/projects/{project}/leave", {
          params: { path: { project: project.slug } },
        }),
      ),
    onSuccess: async () => {
      setLeaveOpen(false);
      queryClient.removeQueries({ queryKey: queryKeys.project(project.slug) });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
      toast.success(`Left ${project.name}`);
      await navigate({ to: "/", replace: true });
    },
    onError: (err) => {
      setLeaveOpen(false);
      handleApiError(err, "Could not leave project");
    },
  });

  const createInviteMutation = useMutation({
    mutationFn: async (vars: { email: string; role: ProjectRole }) =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/invites", {
          params: { path: { project: project.slug } },
          body: vars,
        }),
      ),
    onSuccess: async (res) => {
      setInviteError(null);
      setCreatedInvite(res);
      await queryClient.invalidateQueries({ queryKey: queryKeys.projectInvites(project.slug) });
      toast.success(`Invite created for ${res.invite.email}`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setInviteError(first);
          return;
        }
      }
      setInviteError(getApiErrorMessage(err, "Failed to create invite"));
    },
  });

  const revokeInviteMutation = useMutation({
    mutationFn: async (invite: ProjectInvite) =>
      unwrapVoid(
        await api.DELETE("/api/v1/projects/{project}/invites/{id}", {
          params: { path: { project: project.slug, id: invite.id } },
        }),
      ),
    onSuccess: async (_data, invite) => {
      setRevokingInvite(null);
      toast.success(`Revoked invite for ${invite.email}`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.projectInvites(project.slug) });
    },
    onError: (err) => {
      setRevokingInvite(null);
      handleApiError(err, "Failed to revoke invite");
    },
  });

  if (!isServer) {
    return null;
  }

  const members = membersQuery.data ?? [];
  const invites = invitesQuery.data ?? [];
  // Audit M4: accepted invites duplicate the members table, so only pending
  // invites show by default; "Show accepted" reveals the full history.
  const hasInviteHistory = invites.some((inv) => inv.status !== "pending");
  const visibleInvites = showAccepted ? invites : invites.filter((inv) => inv.status === "pending");
  const isExplicitMember = members.some((m) => m.user.id === me?.user.id);
  const now = new Date();

  const handleInviteOpenChange = (open: boolean) => {
    onInviteOpenChange(open);
    if (!open) {
      // Reset on close so the next "Invite people" starts from a clean form.
      setInviteEmail("");
      setInviteRole("viewer");
      setInviteError(null);
      setCreatedInvite(null);
    }
  };

  const closeInvite = () => handleInviteOpenChange(false);

  const handleInviteSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = inviteEmail.trim();
    if (!trimmed) {
      setInviteError("Enter an email address.");
      return;
    }
    setInviteError(null);
    createInviteMutation.mutate({ email: trimmed, role: inviteRole });
  };

  return (
    <>
      <Card aria-labelledby="members-heading">
        <CardHeader
          titleId="members-heading"
          title="Members"
          description={`People with explicit access to ${project.name}. Instance admins always have owner access.`}
          actions={
            isExplicitMember ? (
              <Button variant="outline" size="sm" onClick={() => setLeaveOpen(true)}>
                <LogOut className="size-3.5" aria-hidden="true" />
                <span>Leave project</span>
              </Button>
            ) : null
          }
        />

        {membersQuery.isPending ? (
          <div className="flex flex-col gap-2 border-t border-border p-5">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : membersQuery.error ? (
          <div role="alert" className="border-t border-border p-5 text-[13px] text-destructive">
            Failed to load members: {getApiErrorMessage(membersQuery.error)}
          </div>
        ) : members.length === 0 ? (
          <div className="border-t border-border p-5 text-[13px] text-muted-foreground">
            No explicit members yet. Invite a team member or client viewer to grant access.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Member</TableHead>
                  <TableHead className="w-36">Role</TableHead>
                  <TableHead className="w-28">Joined</TableHead>
                  {isOwner ? (
                    <TableHead className="w-28 text-right">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  ) : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.map((member) => {
                  const isSelf = member.user.id === me?.user.id;
                  const joinedRel = formatRelativeTime(member.created_at, now, clock24h);
                  const joinedExact = formatExactTimestamp(member.created_at, clock24h).combined;
                  return (
                    <TableRow key={member.user.id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-foreground">{member.user.name}</span>
                          {isSelf ? <Badge variant="neutral">You</Badge> : null}
                        </div>
                        <div className="font-mono text-xs text-muted-foreground">
                          {member.user.email}
                        </div>
                      </TableCell>
                      <TableCell>
                        {isOwner ? (
                          <NativeSelect
                            controlSize="sm"
                            aria-label={`Role for ${member.user.email}`}
                            value={member.role}
                            disabled={updateRoleMutation.isPending}
                            onChange={(e) => {
                              const nextRole = e.target.value as ProjectRole;
                              const isDemotingOwner =
                                member.role === "owner" && nextRole !== "owner";
                              if (isSelf && isDemotingOwner) {
                                setPendingRoleChange({ member, role: nextRole });
                                return;
                              }
                              updateRoleMutation.mutate({
                                userId: member.user.id,
                                role: nextRole,
                              });
                            }}
                            wrapperClassName="w-28"
                          >
                            <option value="owner">owner</option>
                            <option value="member">member</option>
                            <option value="viewer">viewer</option>
                          </NativeSelect>
                        ) : (
                          <RoleBadge role={member.role} />
                        )}
                      </TableCell>
                      <TableCell
                        className="text-xs text-muted-foreground tabular-nums"
                        title={joinedExact}
                      >
                        {joinedRel}
                      </TableCell>
                      {isOwner ? (
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="xs"
                            aria-label={`Remove ${member.user.email}`}
                            className="hover:bg-destructive-subtle hover:text-destructive"
                            onClick={() => setRemovingMember(member)}
                          >
                            <UserMinus className="size-3.5" aria-hidden="true" />
                            <span>Remove</span>
                          </Button>
                        </TableCell>
                      ) : null}
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </Card>

      {isOwner ? (
        <Card aria-labelledby="invites-heading">
          <CardHeader
            titleId="invites-heading"
            title="Invites"
            description="Invite links are single-use and shared manually. Creating a new invite for the same email revokes any pending invite."
            actions={
              hasInviteHistory ? (
                <label
                  htmlFor={showAcceptedId}
                  className="inline-flex h-8 cursor-pointer items-center gap-2 text-[13px] font-medium text-foreground"
                >
                  <input
                    id={showAcceptedId}
                    type="checkbox"
                    checked={showAccepted}
                    onChange={(e) => setShowAccepted(e.target.checked)}
                    className="size-3.5 accent-accent"
                  />
                  Show accepted
                </label>
              ) : null
            }
          />

          {invitesQuery.isPending ? (
            <div className="flex flex-col gap-2 border-t border-border p-5">
              <Skeleton className="h-8 w-full" />
            </div>
          ) : invitesQuery.error ? (
            <div role="alert" className="border-t border-border p-5 text-[13px] text-destructive">
              Failed to load invites: {getApiErrorMessage(invitesQuery.error)}
            </div>
          ) : visibleInvites.length === 0 ? (
            <div className="border-t border-border p-5 text-[13px] text-muted-foreground">
              {invites.length === 0
                ? "No invitations have been created for this project yet."
                : "No pending invites."}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Email</TableHead>
                    <TableHead className="w-28">Role</TableHead>
                    <TableHead className="w-28">Status</TableHead>
                    <TableHead className="w-36">Expires</TableHead>
                    <TableHead className="w-28 text-right">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleInvites.map((inv) => {
                    const expExact = formatExactTimestamp(inv.expires_at, clock24h).combined;
                    return (
                      <TableRow key={inv.id}>
                        <TableCell>
                          <div className="font-mono text-xs text-foreground">{inv.email}</div>
                          {inv.created_by ? (
                            <div className="text-xs text-muted-foreground">
                              Invited by {inv.created_by}
                            </div>
                          ) : null}
                        </TableCell>
                        <TableCell>
                          <RoleBadge role={inv.role} />
                        </TableCell>
                        <TableCell>
                          <Badge variant={inviteStatusBadgeVariant(inv.status)} dot>
                            {inv.status}
                          </Badge>
                        </TableCell>
                        <TableCell
                          className="text-xs whitespace-nowrap text-muted-foreground tabular-nums"
                          title={expExact}
                        >
                          {formatExactTimestamp(inv.expires_at, clock24h).local}
                        </TableCell>
                        <TableCell className="text-right">
                          {inv.status === "pending" ? (
                            <Button
                              variant="ghost"
                              size="xs"
                              aria-label={`Revoke invite for ${inv.email}`}
                              className="hover:bg-destructive-subtle hover:text-destructive"
                              onClick={() => setRevokingInvite(inv)}
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                              <span>Revoke</span>
                            </Button>
                          ) : (
                            <span className="text-xs text-muted-foreground">—</span>
                          )}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </Card>
      ) : null}

      <Card aria-labelledby="client-view-heading">
        <CardHeader
          titleId="client-view-heading"
          title="Client view mode"
          description={
            <>
              Project viewers get a simplified, read-only inbox without delete actions, tag editing,
              or settings.{" "}
              {project.settings.viewer_hide_technical
                ? "Technical tabs (HTML source, Headers, Raw, Tech) and .eml downloads are currently hidden from viewers."
                : "Technical tabs are currently visible to viewers."}
            </>
          }
          actions={
            isOwner ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setPreviewAsViewer(true);
                  void navigate({ to: "/p/$project", params: { project: project.slug } });
                }}
              >
                <Eye className="size-3.5" aria-hidden="true" />
                <span>Preview as viewer</span>
              </Button>
            ) : null
          }
        />
      </Card>

      <Dialog
        open={inviteOpen}
        onOpenChange={handleInviteOpenChange}
        title={createdInvite ? "Invitation link ready" : `Invite to ${project.name}`}
        description={
          createdInvite
            ? "Share this single-use link directly with the recipient. Mailcubby does not send emails."
            : "Create a single-use invite link for a team member or client."
        }
      >
        {createdInvite ? (
          <div className="flex flex-col gap-4">
            <div className="flex items-start gap-2 rounded-md border border-success/30 bg-success-subtle px-3 py-2.5 text-xs text-success-foreground">
              <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <div>
                Invite created for{" "}
                <strong className="font-medium">{createdInvite.invite.email}</strong> as{" "}
                <strong className="font-medium">{createdInvite.invite.role}</strong>. This link is
                shown once.
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="created-invite-url" className="text-xs font-medium text-foreground">
                Invite link
              </label>
              <div className="flex items-center gap-2">
                <Input
                  id="created-invite-url"
                  readOnly
                  value={createdInvite.url}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  type="button"
                  variant="primary"
                  size="sm"
                  onClick={() => void copyToClipboard(createdInvite.url, "invite link")}
                >
                  <Copy className="size-3.5" aria-hidden="true" />
                  <span>Copy</span>
                </Button>
              </div>
            </div>

            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={closeInvite}>
                Done
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleInviteSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor={emailId} className="text-xs font-medium text-foreground">
                Email address
              </label>
              <Input
                id={emailId}
                type="email"
                autoFocus
                required
                placeholder="client@example.com"
                value={inviteEmail}
                onChange={(e) => {
                  setInviteEmail(e.target.value);
                  if (inviteError) {
                    setInviteError(null);
                  }
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor={roleId} className="text-xs font-medium text-foreground">
                Role
              </label>
              <NativeSelect
                id={roleId}
                value={inviteRole}
                onChange={(e) => setInviteRole(e.target.value as ProjectRole)}
                wrapperClassName="w-full"
              >
                {ROLE_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </NativeSelect>
              <p className="text-xs text-muted-foreground">
                {ROLE_OPTIONS.find((opt) => opt.value === inviteRole)?.description}
              </p>
            </div>

            {inviteError ? (
              <p role="alert" className="text-xs font-medium text-destructive">
                {inviteError}
              </p>
            ) : null}

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button type="button" variant="outline" size="sm" onClick={closeInvite}>
                Cancel
              </Button>
              <Button
                type="submit"
                variant="primary"
                size="sm"
                disabled={createInviteMutation.isPending}
              >
                {createInviteMutation.isPending ? "Creating..." : "Create invite link"}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      <ConfirmDialog
        open={Boolean(pendingRoleChange)}
        onOpenChange={(open) => {
          if (!open) {
            setPendingRoleChange(null);
          }
        }}
        title={
          pendingRoleChange ? `Change your role to ${pendingRoleChange.role}?` : "Change your role?"
        }
        description={
          pendingRoleChange
            ? `You will give up owner permissions on ${project.name}${
                pendingRoleChange.role === "viewer"
                  ? " and become read-only."
                  : " and will no longer be able to manage settings, members, or SMTP credentials."
              }`
            : ""
        }
        confirmLabel="Change role"
        destructive
        pending={updateRoleMutation.isPending}
        onConfirm={() => {
          if (pendingRoleChange) {
            updateRoleMutation.mutate({
              userId: pendingRoleChange.member.user.id,
              role: pendingRoleChange.role,
            });
          }
        }}
      />

      <ConfirmDialog
        open={Boolean(removingMember)}
        onOpenChange={(open) => {
          if (!open) {
            setRemovingMember(null);
          }
        }}
        title={removingMember ? `Remove ${removingMember.user.name}?` : "Remove member?"}
        description={
          removingMember
            ? `${removingMember.user.email} will immediately lose access to ${project.name}.`
            : ""
        }
        confirmLabel="Remove member"
        destructive
        pending={removeMemberMutation.isPending}
        onConfirm={() => {
          if (removingMember) {
            removeMemberMutation.mutate(removingMember);
          }
        }}
      />

      <ConfirmDialog
        open={Boolean(revokingInvite)}
        onOpenChange={(open) => {
          if (!open) {
            setRevokingInvite(null);
          }
        }}
        title={revokingInvite ? `Revoke invite for ${revokingInvite.email}?` : "Revoke invite?"}
        description="The invite link will stop working immediately."
        confirmLabel="Revoke invite"
        destructive
        pending={revokeInviteMutation.isPending}
        onConfirm={() => {
          if (revokingInvite) {
            revokeInviteMutation.mutate(revokingInvite);
          }
        }}
      />

      <ConfirmDialog
        open={leaveOpen}
        onOpenChange={setLeaveOpen}
        title={`Leave "${project.name}"?`}
        description="You will lose access to this project unless invited again (unless you are an instance admin)."
        confirmLabel="Leave project"
        destructive
        pending={leaveMutation.isPending}
        onConfirm={() => leaveMutation.mutate()}
      />
    </>
  );
}

export function MembersSettingsPage({ projectSlug }: { projectSlug: string }) {
  const { data: meta } = useQuery(metaQueryOptions());
  const [inviteOpen, setInviteOpen] = useState(false);
  const isServer = meta?.mode === "server";

  return (
    <ProjectSettingsLayout
      projectSlug={projectSlug}
      section="members"
      title="Members & invites"
      // Audit M5: only owners can invite or change roles, so other roles get a plain subtitle.
      description={(project) =>
        project.role === "owner"
          ? "Manage who has access to this project and share invite links."
          : `People with access to ${project.name}.`
      }
      actions={(project) =>
        isServer && project.role === "owner" ? (
          <Button variant="primary" size="sm" onClick={() => setInviteOpen(true)}>
            <MailPlus className="size-3.5" aria-hidden="true" />
            <span>Invite people</span>
          </Button>
        ) : null
      }
    >
      {(project) => (
        <MembersSettingsContent
          project={project}
          inviteOpen={inviteOpen}
          onInviteOpenChange={setInviteOpen}
        />
      )}
    </ProjectSettingsLayout>
  );
}
