import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Monitor, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { AccountLayout } from "@/features/account/AccountLayout";
import { PasswordInput } from "@/features/auth/PasswordInput";
import {
  ApiError,
  api,
  handleApiError,
  type SessionSummary,
  setCsrfToken,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { formatExactTimestamp, formatRelativeTime } from "@/lib/format";
import { useClock24hPreference } from "@/lib/preferences";
import { authSessionsQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

export function SecurityPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: sessions, isLoading: sessionsLoading } = useQuery({
    ...authSessionsQueryOptions(),
    enabled: meta?.mode === "server",
  });
  const [clock24h] = useClock24hPreference();

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (meta && meta.mode !== "server") {
      void navigate({ to: "/account/preferences", replace: true });
    }
  }, [meta, navigate]);

  useEffect(() => {
    document.title = `Security · ${meta?.instance_name || "Mailcubby"}`;
  }, [meta?.instance_name]);

  const changePasswordMutation = useMutation({
    mutationFn: async (vars: { current_password: string; new_password: string }) =>
      unwrap(
        await api.PATCH("/api/v1/auth/me", {
          body: vars,
        }),
      ),
    onSuccess: async () => {
      setCurrentPassword("");
      setNewPassword("");
      setFieldErrors({});
      await queryClient.invalidateQueries({ queryKey: queryKeys.authSessions() });
      toast.success("Password updated and all other sessions signed out");
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 422 && err.details) {
        setFieldErrors(err.details);
        return;
      }
      handleApiError(err, "Failed to change password");
    },
  });

  const revokeSessionMutation = useMutation({
    mutationFn: async (session: SessionSummary) => {
      unwrapVoid(
        await api.DELETE("/api/v1/auth/sessions/{id}", {
          params: { path: { id: session.id } },
        }),
      );
      return session;
    },
    onSuccess: async (revoked) => {
      if (revoked.current) {
        setCsrfToken("");
        queryClient.clear();
        await navigate({ to: "/login", search: { returnTo: undefined } });
        return;
      }
      await queryClient.invalidateQueries({ queryKey: queryKeys.authSessions() });
      toast.success("Session revoked");
    },
    onError: (err) => {
      handleApiError(err, "Failed to revoke session");
    },
  });

  const handlePasswordSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!currentPassword) {
      errs.current_password = "Enter your current password.";
    }
    if (newPassword.length < 10 || newPassword.length > 256) {
      errs.new_password = "New password must be between 10 and 256 characters.";
    }
    if (Object.keys(errs).length > 0) {
      setFieldErrors(errs);
      return;
    }
    setFieldErrors({});
    changePasswordMutation.mutate({
      current_password: currentPassword,
      new_password: newPassword,
    });
  };

  return (
    <AccountLayout
      section="security"
      title="Security"
      description="Update your password and manage active browser sessions."
    >
      <div className="flex flex-col gap-4">
        <Card aria-labelledby="change-password-heading">
          <CardHeader
            title="Change password"
            titleId="change-password-heading"
            description="Changing your password signs out all other active sessions."
          />

          <form
            onSubmit={handlePasswordSubmit}
            method="post"
            aria-labelledby="change-password-heading"
            noValidate
          >
            <CardContent className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <label htmlFor="current-password" className="text-xs font-medium text-foreground">
                  Current password
                </label>
                <PasswordInput
                  id="current-password"
                  name="current-password"
                  autoComplete="current-password"
                  enterKeyHint="next"
                  required
                  value={currentPassword}
                  onChange={(e) => {
                    setCurrentPassword(e.target.value);
                    if (fieldErrors.current_password) {
                      setFieldErrors((prev) => {
                        const next = { ...prev };
                        delete next.current_password;
                        return next;
                      });
                    }
                  }}
                  aria-invalid={fieldErrors.current_password ? "true" : undefined}
                  aria-describedby={
                    fieldErrors.current_password ? "current-password-error" : undefined
                  }
                />
                {fieldErrors.current_password ? (
                  <p id="current-password-error" className="text-xs text-destructive">
                    {fieldErrors.current_password}
                  </p>
                ) : null}
              </div>

              <div className="flex flex-col gap-1.5">
                <label htmlFor="new-password" className="text-xs font-medium text-foreground">
                  New password
                </label>
                <PasswordInput
                  id="new-password"
                  name="new-password"
                  autoComplete="new-password"
                  enterKeyHint="done"
                  minLength={10}
                  maxLength={256}
                  required
                  value={newPassword}
                  onChange={(e) => {
                    setNewPassword(e.target.value);
                    if (fieldErrors.new_password) {
                      setFieldErrors((prev) => {
                        const next = { ...prev };
                        delete next.new_password;
                        return next;
                      });
                    }
                  }}
                  aria-invalid={fieldErrors.new_password ? "true" : undefined}
                  aria-describedby="new-password-hint"
                />
                <p
                  id="new-password-hint"
                  className={
                    fieldErrors.new_password
                      ? "text-xs text-destructive"
                      : "text-xs text-muted-foreground"
                  }
                >
                  {fieldErrors.new_password ?? "Minimum 10 characters."}
                </p>
              </div>
            </CardContent>

            <CardFooter>
              <Button
                type="submit"
                variant="primary"
                size="sm"
                disabled={changePasswordMutation.isPending}
              >
                {changePasswordMutation.isPending ? "Updating..." : "Update password"}
              </Button>
            </CardFooter>
          </form>
        </Card>

        <Card aria-labelledby="active-sessions-heading">
          <CardHeader
            title="Active sessions"
            titleId="active-sessions-heading"
            description="Browsers currently signed in to your account. Revoke any session you do not recognise."
          />

          <CardContent>
            {sessionsLoading ? (
              <div className="flex flex-col gap-2">
                <Skeleton className="h-14 w-full" />
                <Skeleton className="h-14 w-full" />
              </div>
            ) : sessions && sessions.length > 0 ? (
              <ul className="flex flex-col divide-y divide-border rounded-md border border-border bg-background">
                {sessions.map((sess) => {
                  const lastSeen = formatRelativeTime(sess.last_seen_at, new Date(), clock24h);
                  const lastSeenExact = formatExactTimestamp(sess.last_seen_at, clock24h);
                  const createdExact = formatExactTimestamp(sess.created_at, clock24h);
                  return (
                    <li
                      key={sess.id}
                      className="flex flex-wrap items-center justify-between gap-3 px-3.5 py-3"
                    >
                      <div className="flex min-w-0 flex-1 basis-64 items-start gap-2.5">
                        <Monitor
                          className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                          aria-hidden="true"
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex min-w-0 items-center gap-2">
                            <span
                              className="min-w-0 truncate text-[13px] font-medium text-foreground"
                              title={sess.user_agent || undefined}
                            >
                              {sess.user_agent || "Unknown browser"}
                            </span>
                            {sess.current ? <Badge variant="accent">Current</Badge> : null}
                          </div>
                          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
                            <span>IP: {sess.ip || "Unknown"}</span>
                            <span>·</span>
                            <span title={lastSeenExact.combined}>Last active {lastSeen}</span>
                            <span>·</span>
                            <span title={createdExact.combined}>Started {createdExact.local}</span>
                          </div>
                        </div>
                      </div>

                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={revokeSessionMutation.isPending}
                        onClick={() => revokeSessionMutation.mutate(sess)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        <span>{sess.current ? "Sign out" : "Revoke"}</span>
                      </Button>
                    </li>
                  );
                })}
              </ul>
            ) : (
              <p className="text-xs text-muted-foreground">No active sessions found.</p>
            )}
          </CardContent>
        </Card>
      </div>
    </AccountLayout>
  );
}
