import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  CheckCircle2,
  Copy,
  KeyRound,
  Search,
  Trash2,
  UserCheck,
  UserPlus,
} from "lucide-react";
import { type FormEvent, useEffect, useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog, Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { AdminLayout } from "@/features/admin/AdminLayout";
import {
  type AdminCreateUserResponse,
  type AdminPasswordResetResponse,
  type AdminUpdateUserRequest,
  type AdminUser,
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { formatCount, formatExactTimestamp, formatRelativeTime } from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

export function AdminUsersPage() {
  const queryClient = useQueryClient();
  const [clock24h] = useClock24hPreference();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());

  const isAdmin = Boolean(meta?.mode === "server" && me?.user.is_admin);

  const [searchInput, setSearchInput] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newEmail, setNewEmail] = useState("");
  const [newIsAdmin, setNewIsAdmin] = useState(false);
  const [newCanCreate, setNewCanCreate] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [createdUserResult, setCreatedUserResult] = useState<AdminCreateUserResponse | null>(null);

  const [resetResult, setResetResult] = useState<{
    user: AdminUser;
    data: AdminPasswordResetResponse;
  } | null>(null);
  const [disablingUser, setDisablingUser] = useState<AdminUser | null>(null);
  const [deletingUser, setDeletingUser] = useState<AdminUser | null>(null);

  const searchId = useId();
  const nameId = useId();
  const emailId = useId();
  const isAdminId = useId();
  const canCreateId = useId();

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(searchInput.trim()), 200);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const usersQuery = useInfiniteQuery({
    queryKey: queryKeys.adminUsers(debouncedQuery ? { q: debouncedQuery } : undefined),
    initialPageParam: undefined as string | undefined,
    enabled: isAdmin,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/api/v1/admin/users", {
          params: {
            query: {
              limit: 50,
              ...(pageParam ? { cursor: pageParam } : {}),
              ...(debouncedQuery ? { q: debouncedQuery } : {}),
            },
          },
        }),
      ),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });

  const users = useMemo(
    () => usersQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [usersQuery.data],
  );

  const invalidateUsers = async () => {
    await queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
  };

  const createUserMutation = useMutation({
    mutationFn: async (vars: {
      name: string;
      email: string;
      is_admin: boolean;
      can_create_projects: boolean;
    }) =>
      unwrap(
        await api.POST("/api/v1/admin/users", {
          body: vars,
        }),
      ),
    onSuccess: async (res) => {
      setCreateError(null);
      setCreatedUserResult(res);
      await invalidateUsers();
      toast.success(`Created user ${res.user.email}`);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setCreateError(first);
          return;
        }
      }
      setCreateError(getApiErrorMessage(err, "Failed to create user"));
    },
  });

  const updateUserMutation = useMutation({
    mutationFn: async ({ id, body }: { id: number; body: AdminUpdateUserRequest }) =>
      unwrap(
        await api.PATCH("/api/v1/admin/users/{id}", {
          params: { path: { id } },
          body,
        }),
      ),
    onSuccess: async (updated, vars) => {
      setDisablingUser(null);
      await invalidateUsers();
      if (updated.id === me?.user.id) {
        await queryClient.invalidateQueries({ queryKey: queryKeys.authMe() });
      }
      if (vars.body.disabled === true) {
        toast.success(`Disabled ${updated.email}`);
      } else if (vars.body.disabled === false) {
        toast.success(`Enabled ${updated.email}`);
      } else {
        toast.success(`Updated ${updated.email}`);
      }
    },
    onError: (err) => {
      setDisablingUser(null);
      handleApiError(err, "Failed to update user");
    },
  });

  const deleteUserMutation = useMutation({
    mutationFn: async (user: AdminUser) =>
      unwrapVoid(
        await api.DELETE("/api/v1/admin/users/{id}", {
          params: { path: { id: user.id } },
        }),
      ),
    onSuccess: async (_data, user) => {
      setDeletingUser(null);
      await invalidateUsers();
      toast.success(`Deleted ${user.email}`);
    },
    onError: (err) => {
      setDeletingUser(null);
      handleApiError(err, "Failed to delete user");
    },
  });

  const passwordResetMutation = useMutation({
    mutationFn: async (user: AdminUser) => ({
      user,
      data: unwrap(
        await api.POST("/api/v1/admin/users/{id}/password-reset", {
          params: { path: { id: user.id } },
        }),
      ),
    }),
    onSuccess: (res) => {
      setResetResult(res);
      toast.success(`Password reset link generated for ${res.user.email}`);
    },
    onError: (err) => {
      handleApiError(err, "Failed to generate password reset link");
    },
  });

  const handleCreateSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmedName = newName.trim();
    const trimmedEmail = newEmail.trim();
    if (!trimmedName) {
      setCreateError("Full name is required.");
      return;
    }
    if (!trimmedEmail) {
      setCreateError("Email address is required.");
      return;
    }
    setCreateError(null);
    createUserMutation.mutate({
      name: trimmedName,
      email: trimmedEmail,
      is_admin: newIsAdmin,
      can_create_projects: newCanCreate,
    });
  };

  const now = new Date();

  return (
    <AdminLayout
      section="users"
      title="Users"
      description="Create accounts, manage instance permissions, and issue password reset links."
      actions={
        <Button
          variant="default"
          size="sm"
          onClick={() => {
            setNewName("");
            setNewEmail("");
            setNewIsAdmin(false);
            setNewCanCreate(false);
            setCreateError(null);
            setCreatedUserResult(null);
            setCreateOpen(true);
          }}
        >
          <UserPlus className="size-3.5" aria-hidden="true" />
          <span>Invite user</span>
        </Button>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="relative w-full max-w-xs">
            <label htmlFor={searchId} className="sr-only">
              Search users
            </label>
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              id={searchId}
              type="search"
              placeholder="Search by name or email..."
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              className="pl-8"
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatCount(users.length)} {users.length === 1 ? "user" : "users"}
            {usersQuery.hasNextPage ? "+" : ""}
          </span>
        </div>

        <section className="rounded-lg border border-border bg-surface">
          {usersQuery.isPending ? (
            <div className="flex flex-col gap-2 p-5">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : usersQuery.error ? (
            <div role="alert" className="p-5 text-xs text-destructive">
              Failed to load users: {getApiErrorMessage(usersQuery.error)}
            </div>
          ) : users.length === 0 ? (
            <div className="p-6 text-center text-xs text-muted-foreground">
              {debouncedQuery ? `No users match "${debouncedQuery}".` : "No users found."}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-[13px]">
                <thead>
                  <tr className="border-b border-border bg-background/50 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                    <th scope="col" className="px-4 py-2.5">
                      User
                    </th>
                    <th scope="col" className="px-3 py-2.5">
                      Admin
                    </th>
                    <th scope="col" className="px-3 py-2.5">
                      Create projects
                    </th>
                    <th scope="col" className="px-3 py-2.5 text-right">
                      Projects
                    </th>
                    <th scope="col" className="px-3 py-2.5">
                      Last login
                    </th>
                    <th scope="col" className="px-4 py-2.5 text-right">
                      Actions
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {users.map((user) => {
                    const isSelf = user.id === me?.user.id;
                    const isDisabled = user.disabled_at !== null;
                    const lastLoginRel = user.last_login_at
                      ? formatRelativeTime(user.last_login_at, now, clock24h)
                      : null;
                    const lastLoginExact = user.last_login_at
                      ? formatExactTimestamp(user.last_login_at, clock24h).combined
                      : undefined;

                    return (
                      <tr key={user.id} className="hover:bg-muted/30">
                        <td className="px-4 py-3">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-medium text-foreground">{user.name}</span>
                            {isSelf ? <Badge variant="secondary">You</Badge> : null}
                            {!user.has_password ? (
                              <Badge variant="outline">Pending invite</Badge>
                            ) : null}
                            {isDisabled ? <Badge variant="destructive">Disabled</Badge> : null}
                          </div>
                          <div className="font-mono text-xs text-muted-foreground">
                            {user.email}
                          </div>
                        </td>

                        <td className="px-3 py-3">
                          <input
                            type="checkbox"
                            role="switch"
                            aria-checked={user.is_admin}
                            aria-label={`Instance admin for ${user.email}`}
                            checked={user.is_admin}
                            disabled={updateUserMutation.isPending}
                            onChange={(e) =>
                              updateUserMutation.mutate({
                                id: user.id,
                                body: { is_admin: e.target.checked },
                              })
                            }
                            className="size-4 cursor-pointer accent-accent disabled:cursor-not-allowed"
                          />
                        </td>

                        <td className="px-3 py-3">
                          <input
                            type="checkbox"
                            role="switch"
                            aria-checked={user.can_create_projects}
                            aria-label={`Can create projects for ${user.email}`}
                            checked={user.can_create_projects}
                            disabled={updateUserMutation.isPending}
                            onChange={(e) =>
                              updateUserMutation.mutate({
                                id: user.id,
                                body: { can_create_projects: e.target.checked },
                              })
                            }
                            className="size-4 cursor-pointer accent-accent disabled:cursor-not-allowed"
                          />
                        </td>

                        <td className="px-3 py-3 text-right text-xs text-muted-foreground tabular-nums">
                          {formatCount(user.project_count)}
                        </td>

                        <td
                          className="px-3 py-3 text-xs text-muted-foreground tabular-nums"
                          title={lastLoginExact}
                        >
                          {lastLoginRel ?? "Never"}
                        </td>

                        <td className="px-4 py-3 text-right">
                          <div className="inline-flex items-center gap-1">
                            <Button
                              variant="ghost"
                              size="xs"
                              title="Generate password reset link"
                              aria-label={`Reset password for ${user.email}`}
                              disabled={passwordResetMutation.isPending}
                              onClick={() => passwordResetMutation.mutate(user)}
                            >
                              <KeyRound className="size-3.5" aria-hidden="true" />
                              <span className="hidden lg:inline">Reset link</span>
                            </Button>

                            {isDisabled ? (
                              <Button
                                variant="ghost"
                                size="xs"
                                aria-label={`Enable ${user.email}`}
                                disabled={updateUserMutation.isPending}
                                onClick={() =>
                                  updateUserMutation.mutate({
                                    id: user.id,
                                    body: { disabled: false },
                                  })
                                }
                              >
                                <UserCheck className="size-3.5" aria-hidden="true" />
                                <span className="hidden lg:inline">Enable</span>
                              </Button>
                            ) : (
                              <Button
                                variant="ghost"
                                size="xs"
                                aria-label={`Disable ${user.email}`}
                                className="text-muted-foreground hover:text-destructive"
                                onClick={() => setDisablingUser(user)}
                              >
                                <Ban className="size-3.5" aria-hidden="true" />
                                <span className="hidden lg:inline">Disable</span>
                              </Button>
                            )}

                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={`Delete ${user.email}`}
                              title={`Delete ${user.email}`}
                              className="size-7 text-muted-foreground hover:text-destructive"
                              onClick={() => setDeletingUser(user)}
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          {usersQuery.hasNextPage ? (
            <div className="border-t border-border p-3 text-center">
              <Button
                variant="outline"
                size="xs"
                disabled={usersQuery.isFetchingNextPage}
                onClick={() => void usersQuery.fetchNextPage()}
              >
                {usersQuery.isFetchingNextPage ? "Loading..." : "Load more users"}
              </Button>
            </div>
          ) : null}
        </section>
      </div>

      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open);
          if (!open) {
            setCreatedUserResult(null);
            setCreateError(null);
          }
        }}
        title={createdUserResult ? "User invite link ready" : "Invite user"}
        description={
          createdUserResult
            ? "Share this single-use link directly with the user so they can set their password."
            : "Create a user account and generate a one-time setup invite link."
        }
      >
        {createdUserResult ? (
          <div className="flex flex-col gap-4">
            <div className="flex items-start gap-2 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2.5 text-xs text-emerald-700 dark:text-emerald-400">
              <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              <div>
                Created <strong className="font-medium">{createdUserResult.user.name}</strong> (
                <span className="font-mono">{createdUserResult.user.email}</span>). This link is
                shown once.
              </div>
            </div>

            <div className="flex flex-col gap-1.5">
              <label
                htmlFor="created-user-invite-url"
                className="text-xs font-medium text-foreground"
              >
                Invite link
              </label>
              <div className="flex items-center gap-2">
                <Input
                  id="created-user-invite-url"
                  readOnly
                  value={createdUserResult.invite_url}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  type="button"
                  variant="default"
                  size="sm"
                  onClick={() =>
                    void copyToClipboard(createdUserResult.invite_url, "user invite link")
                  }
                >
                  <Copy className="size-3.5" aria-hidden="true" />
                  <span>Copy</span>
                </Button>
              </div>
            </div>

            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={() => setCreateOpen(false)}>
                Done
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleCreateSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor={nameId} className="text-xs font-medium text-foreground">
                Full name
              </label>
              <Input
                id={nameId}
                autoFocus
                required
                maxLength={80}
                placeholder="Jane Doe"
                value={newName}
                onChange={(e) => {
                  setNewName(e.target.value);
                  if (createError) {
                    setCreateError(null);
                  }
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor={emailId} className="text-xs font-medium text-foreground">
                Email address
              </label>
              <Input
                id={emailId}
                type="email"
                required
                placeholder="jane@agency.test"
                value={newEmail}
                onChange={(e) => {
                  setNewEmail(e.target.value);
                  if (createError) {
                    setCreateError(null);
                  }
                }}
              />
            </div>

            <div className="flex flex-col gap-2.5 rounded-md border border-border bg-background/60 p-3">
              <label
                htmlFor={canCreateId}
                className="flex cursor-pointer items-start gap-2.5 text-xs"
              >
                <input
                  id={canCreateId}
                  type="checkbox"
                  checked={newCanCreate}
                  onChange={(e) => setNewCanCreate(e.target.checked)}
                  className="mt-0.5 size-3.5 accent-accent"
                />
                <span>
                  <span className="font-medium text-foreground">Can create projects</span>
                  <span className="block text-muted-foreground">
                    Allows this user to create new projects and become their owner.
                  </span>
                </span>
              </label>

              <label
                htmlFor={isAdminId}
                className="flex cursor-pointer items-start gap-2.5 text-xs"
              >
                <input
                  id={isAdminId}
                  type="checkbox"
                  checked={newIsAdmin}
                  onChange={(e) => setNewIsAdmin(e.target.checked)}
                  className="mt-0.5 size-3.5 accent-accent"
                />
                <span>
                  <span className="font-medium text-foreground">Instance administrator</span>
                  <span className="block text-muted-foreground">
                    Full access to all projects, users, and instance settings.
                  </span>
                </span>
              </label>
            </div>

            {createError ? (
              <p role="alert" className="text-xs font-medium text-destructive">
                {createError}
              </p>
            ) : null}

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={createUserMutation.isPending}>
                {createUserMutation.isPending ? "Creating..." : "Create user & invite link"}
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      <Dialog
        open={Boolean(resetResult)}
        onOpenChange={(open) => {
          if (!open) {
            setResetResult(null);
          }
        }}
        title="Password reset link"
        description={
          resetResult
            ? `Single-use link for ${resetResult.user.email}. Valid for 1 hour and shown once.`
            : ""
        }
      >
        {resetResult ? (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="reset-link-url" className="text-xs font-medium text-foreground">
                Reset URL
              </label>
              <div className="flex items-center gap-2">
                <Input
                  id="reset-link-url"
                  readOnly
                  value={resetResult.data.url}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  type="button"
                  variant="default"
                  size="sm"
                  onClick={() => void copyToClipboard(resetResult.data.url, "password reset link")}
                >
                  <Copy className="size-3.5" aria-hidden="true" />
                  <span>Copy</span>
                </Button>
              </div>
              <p className="text-[11px] text-muted-foreground">
                Expires at {formatExactTimestamp(resetResult.data.expires_at, clock24h).local}.
              </p>
            </div>
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={() => setResetResult(null)}>
                Done
              </Button>
            </div>
          </div>
        ) : null}
      </Dialog>

      <ConfirmDialog
        open={Boolean(disablingUser)}
        onOpenChange={(open) => {
          if (!open) {
            setDisablingUser(null);
          }
        }}
        title={disablingUser ? `Disable ${disablingUser.name}?` : "Disable user?"}
        description={
          disablingUser
            ? `${disablingUser.email} will be signed out immediately and all of their sessions and API tokens will be revoked.`
            : ""
        }
        confirmLabel="Disable user"
        destructive
        pending={updateUserMutation.isPending}
        onConfirm={() => {
          if (disablingUser) {
            updateUserMutation.mutate({ id: disablingUser.id, body: { disabled: true } });
          }
        }}
      />

      <ConfirmDialog
        open={Boolean(deletingUser)}
        onOpenChange={(open) => {
          if (!open) {
            setDeletingUser(null);
          }
        }}
        title={deletingUser ? `Delete ${deletingUser.name}?` : "Delete user?"}
        description={
          deletingUser
            ? `Permanently delete ${deletingUser.email} and remove all of their project memberships and sessions.`
            : ""
        }
        confirmLabel="Delete user"
        destructive
        pending={deleteUserMutation.isPending}
        onConfirm={() => {
          if (deletingUser) {
            deleteUserMutation.mutate(deletingUser);
          }
        }}
      />
    </AdminLayout>
  );
}
