import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { AlertCircle, LogIn, LogOut, MailCheck, MailX, UserPlus } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/components/EmptyState";
import { RoleBadge } from "@/components/RoleBadge";
import { Button, buttonClassName } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { PasswordInput } from "@/features/auth/PasswordInput";
import {
  type AcceptInviteRequest,
  ApiError,
  api,
  handleApiError,
  setCsrfToken,
  unwrap,
  unwrapVoid,
} from "@/lib/api";
import { formatExactTimestamp } from "@/lib/format";
import { authMeQueryOptions, invitePreviewQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { useSingleProjectSlug } from "@/lib/useSingleProjectSlug";

export interface InviteAcceptPageProps {
  token: string;
}

function formatRateLimitMessage(retryAfterSeconds: number | undefined): string {
  const minutes = retryAfterSeconds ? Math.max(1, Math.ceil(retryAfterSeconds / 60)) : 15;
  return `Too many attempts, try again in ${minutes} ${minutes === 1 ? "minute" : "minutes"}.`;
}

export function InviteAcceptPage({ token }: InviteAcceptPageProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const { data: meta, isPending: metaPending } = useQuery(metaQueryOptions());
  const isServer = meta?.mode === "server";

  const previewQuery = useQuery({
    ...invitePreviewQueryOptions(token),
    enabled: isServer,
  });

  // Check whether the browser already has an active session (401 is expected when logged out).
  const authQuery = useQuery({
    ...authMeQueryOptions(),
    enabled: isServer,
  });

  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [loginRequired, setLoginRequired] = useState(false);

  const nameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  const instanceName = meta?.instance_name || "Mailcubby";
  const preview = previewQuery.data;
  const currentUser = authQuery.data?.user;
  // Audit V1: `/` sends a single-project user straight to that inbox, so the
  // links out of this page name the inbox and open it directly.
  const singleProjectSlug = useSingleProjectSlug();

  useEffect(() => {
    document.title = preview?.project
      ? `Join ${preview.project.name} · ${instanceName}`
      : `Accept invitation · ${instanceName}`;
  }, [preview?.project, instanceName]);

  const logoutMutation = useMutation({
    mutationFn: async () => {
      unwrapVoid(await api.POST("/api/v1/auth/logout"));
    },
    onSuccess: async () => {
      setCsrfToken("");
      queryClient.clear();
      setErrorMessage(null);
      setLoginRequired(false);
      await Promise.all([
        queryClient.refetchQueries({ queryKey: queryKeys.meta() }),
        queryClient.refetchQueries({ queryKey: queryKeys.invitePreview(token) }),
        authQuery.refetch(),
      ]);
    },
    onError: (err) => {
      handleApiError(err, "Failed to sign out");
    },
  });

  const acceptMutation = useMutation({
    mutationFn: async (body: AcceptInviteRequest) =>
      unwrap(
        await api.POST("/api/v1/invites/{token}/accept", {
          params: { path: { token } },
          body,
        }),
      ),
    onSuccess: async (res) => {
      setErrorMessage(null);
      setLoginRequired(false);
      await queryClient.fetchQuery(authMeQueryOptions());
      await queryClient.invalidateQueries({ queryKey: queryKeys.projects() });

      if (res.project) {
        toast.success(`Joined ${res.project.name}`);
        await navigate({
          to: "/p/$project",
          params: { project: res.project.slug },
          replace: true,
        });
      } else {
        toast.success("Account setup complete");
        await navigate({ to: "/", replace: true });
      }
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.status === 409 && err.code === "login_required") {
          setLoginRequired(true);
          // FR-056: also returned for admin-created accounts without a password,
          // which must finish setup via their account invite before signing in.
          setErrorMessage(
            "An account with this email already exists. Sign in to accept this invitation. If you have not set a password yet, first finish account setup with the link an administrator sent you (or ask an administrator for a password reset link), then sign in.",
          );
          return;
        }
        if (err.status === 410) {
          setErrorMessage("This invitation link is invalid, already used, or has expired.");
          return;
        }
        if (err.status === 429) {
          setErrorMessage(formatRateLimitMessage(err.retryAfterSeconds));
          return;
        }
        if (err.details) {
          const first = err.details.password ?? err.details.name ?? Object.values(err.details)[0];
          if (first) {
            setErrorMessage(first);
            return;
          }
        }
        setErrorMessage(err.message);
        return;
      }
      setErrorMessage("Unable to accept invitation. Please try again.");
    },
  });

  const loginReturnTo = `/invite/${encodeURIComponent(token)}`;

  const handleUnauthSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const isInstanceInvite = !preview?.project;
    const trimmedName = name.trim();

    if (!isInstanceInvite && !trimmedName) {
      setErrorMessage("Enter your full name.");
      nameRef.current?.focus();
      return;
    }
    if (trimmedName.length > 80) {
      setErrorMessage("Name must be 80 characters or fewer.");
      nameRef.current?.focus();
      return;
    }
    if (password.length < 10 || password.length > 256) {
      setErrorMessage("Password must be between 10 and 256 characters.");
      passwordRef.current?.focus();
      return;
    }

    setErrorMessage(null);
    setLoginRequired(false);
    const body: AcceptInviteRequest = {
      password,
      ...(trimmedName ? { name: trimmedName } : {}),
    };
    acceptMutation.mutate(body);
  };

  if (metaPending || (isServer && (previewQuery.isPending || authQuery.isPending))) {
    return (
      <main className="flex min-h-full items-center justify-center px-4 py-10">
        <div className="flex w-full max-w-sm flex-col gap-4 rounded-lg border border-border bg-surface p-6 shadow-xs">
          <Skeleton className="mx-auto size-9 rounded-md" />
          <Skeleton className="mx-auto h-5 w-44" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      </main>
    );
  }

  if (!isServer) {
    return (
      <main className="flex min-h-full items-center justify-center px-4 py-10">
        <div className="w-full max-w-sm rounded-lg border border-border bg-surface shadow-xs">
          <EmptyState
            icon={MailX}
            headingLevel={1}
            title="Invitations unavailable"
            description="Invitations are only available when Mailcubby runs in server mode."
            action={
              <Link to="/" className={buttonClassName({ variant: "primary" })}>
                Go to projects
              </Link>
            }
          />
        </div>
      </main>
    );
  }

  if (previewQuery.error || !preview) {
    const err = previewQuery.error;
    const isRateLimited = err instanceof ApiError && err.status === 429;
    const errText = isRateLimited
      ? formatRateLimitMessage(err.retryAfterSeconds)
      : "This invitation link is invalid, already used, or has expired. Ask an owner or administrator for a new invite.";

    return (
      <main className="flex min-h-full items-center justify-center px-4 py-10">
        <div className="w-full max-w-sm rounded-lg border border-border bg-surface shadow-xs">
          <EmptyState
            icon={isRateLimited ? AlertCircle : MailX}
            headingLevel={1}
            title={isRateLimited ? "Too many requests" : "Invitation unavailable"}
            description={<span role="alert">{errText}</span>}
            action={
              currentUser ? (
                singleProjectSlug ? (
                  <Link
                    to="/p/$project"
                    params={{ project: singleProjectSlug }}
                    className={buttonClassName({ variant: "primary" })}
                  >
                    Go to inbox
                  </Link>
                ) : (
                  <Link to="/" className={buttonClassName({ variant: "primary" })}>
                    Go to projects
                  </Link>
                )
              ) : (
                <Link
                  to="/login"
                  search={{ returnTo: undefined }}
                  className={buttonClassName({ variant: "primary" })}
                >
                  Sign in
                </Link>
              )
            }
          />
        </div>
      </main>
    );
  }

  const isInstanceInvite = !preview.project;
  const expiresLocal = formatExactTimestamp(preview.expires_at).local;

  return (
    <main className="flex min-h-full items-center justify-center px-4 py-10">
      <div className="w-full max-w-sm rounded-lg border border-border bg-surface p-6 shadow-xs">
        <div className="mb-5 flex flex-col items-center gap-2 text-center">
          <span className="flex size-9 items-center justify-center rounded-md bg-sidebar text-sidebar-accent">
            {isInstanceInvite ? (
              <UserPlus className="size-4" aria-hidden="true" />
            ) : (
              <MailCheck className="size-4" aria-hidden="true" />
            )}
          </span>
          <div>
            <h1 className="text-base font-semibold tracking-tight text-foreground">
              {isInstanceInvite
                ? `Set up your ${instanceName} account`
                : `Join ${preview.project?.name}`}
            </h1>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {preview.invited_by
                ? `Invited by ${preview.invited_by}`
                : isInstanceInvite
                  ? "Choose a password to activate your account"
                  : `You have been invited to ${instanceName}`}
            </p>
          </div>
        </div>

        <dl className="mb-5 flex flex-col gap-1.5 rounded-md border border-border bg-background/60 p-3 text-xs">
          <div className="flex items-center justify-between gap-2">
            <dt className="text-muted-foreground">Email</dt>
            <dd className="truncate font-mono text-foreground">{preview.email}</dd>
          </div>
          {preview.project ? (
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Project</dt>
              <dd className="truncate font-medium text-foreground">{preview.project.name}</dd>
            </div>
          ) : null}
          {preview.role ? (
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Role</dt>
              <dd>
                <RoleBadge role={preview.role} />
              </dd>
            </div>
          ) : null}
          {expiresLocal ? (
            <div className="flex items-center justify-between gap-2">
              <dt className="text-muted-foreground">Expires</dt>
              <dd className="text-muted-foreground tabular-nums">{expiresLocal}</dd>
            </div>
          ) : null}
        </dl>

        {currentUser ? (
          isInstanceInvite ? (
            <div className="flex flex-col gap-3">
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning-subtle px-3 py-2.5 text-xs text-foreground"
              >
                <AlertCircle className="mt-0.5 size-3.5 shrink-0 text-warning" aria-hidden="true" />
                <span>
                  You are currently signed in as{" "}
                  <strong className="font-medium">{currentUser.email}</strong>. Sign out first to
                  activate this new account invite.
                </span>
              </div>
              <Button
                type="button"
                variant="primary"
                className="w-full"
                disabled={logoutMutation.isPending}
                onClick={() => logoutMutation.mutate()}
              >
                <LogOut className="size-3.5" aria-hidden="true" />
                <span>{logoutMutation.isPending ? "Signing out..." : "Sign out to accept"}</span>
              </Button>
              {singleProjectSlug ? (
                <Link
                  to="/p/$project"
                  params={{ project: singleProjectSlug }}
                  className={buttonClassName({ variant: "outline", className: "w-full" })}
                >
                  Back to inbox
                </Link>
              ) : (
                <Link
                  to="/"
                  className={buttonClassName({ variant: "outline", className: "w-full" })}
                >
                  Back to projects
                </Link>
              )}
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="rounded-md border border-border bg-background px-3 py-2 text-xs text-muted-foreground">
                Signed in as{" "}
                <strong className="font-medium text-foreground">{currentUser.name}</strong> (
                <span className="font-mono">{currentUser.email}</span>). Accepting will add this
                project to your current account.
              </div>

              {errorMessage ? (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive-subtle px-3 py-2 text-xs text-destructive"
                >
                  <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                  <span>{errorMessage}</span>
                </div>
              ) : null}

              <Button
                type="button"
                variant="primary"
                className="w-full"
                disabled={acceptMutation.isPending}
                onClick={() => acceptMutation.mutate({})}
              >
                {acceptMutation.isPending ? "Joining..." : "Accept invitation"}
              </Button>

              <Button
                type="button"
                variant="outline"
                size="sm"
                className="w-full"
                disabled={logoutMutation.isPending}
                onClick={() => logoutMutation.mutate()}
              >
                <LogOut className="size-3.5" aria-hidden="true" />
                <span>Sign out to use a different account</span>
              </Button>
            </div>
          )
        ) : (
          <form
            onSubmit={handleUnauthSubmit}
            method="post"
            className="flex flex-col gap-4"
            noValidate
          >
            <div className="flex flex-col gap-1.5">
              <label htmlFor="invite-name" className="text-xs font-medium text-foreground">
                {isInstanceInvite ? (
                  <>
                    Display name{" "}
                    <span className="font-normal text-muted-foreground">(optional)</span>
                  </>
                ) : (
                  "Full name"
                )}
              </label>
              <Input
                ref={nameRef}
                id="invite-name"
                name="name"
                autoComplete="name"
                maxLength={80}
                required={!isInstanceInvite}
                autoFocus={!isInstanceInvite}
                placeholder={isInstanceInvite ? "Leave blank to keep assigned name" : "Jane Doe"}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (errorMessage) {
                    setErrorMessage(null);
                  }
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="invite-password" className="text-xs font-medium text-foreground">
                Password
              </label>
              <PasswordInput
                ref={passwordRef}
                id="invite-password"
                name="password"
                autoComplete="new-password"
                enterKeyHint="done"
                minLength={10}
                maxLength={256}
                required
                autoFocus={isInstanceInvite}
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  if (errorMessage) {
                    setErrorMessage(null);
                  }
                }}
                aria-describedby="invite-password-hint"
                aria-invalid={errorMessage ? "true" : undefined}
              />
              <p id="invite-password-hint" className="text-[11px] text-muted-foreground">
                Minimum 10 characters.
              </p>
            </div>

            {errorMessage ? (
              <div
                role="alert"
                className="flex flex-col gap-2 rounded-md border border-destructive/30 bg-destructive-subtle px-3 py-2 text-xs text-destructive"
              >
                <div className="flex items-start gap-2">
                  <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                  <span>{errorMessage}</span>
                </div>
                {loginRequired ? (
                  <Link
                    to="/login"
                    search={{ returnTo: loginReturnTo }}
                    className={buttonClassName({ variant: "primary", size: "xs" })}
                  >
                    <LogIn className="size-3.5" aria-hidden="true" />
                    <span>Sign in to accept</span>
                  </Link>
                ) : null}
              </div>
            ) : null}

            <Button
              type="submit"
              variant="primary"
              disabled={acceptMutation.isPending}
              className="w-full"
            >
              {acceptMutation.isPending
                ? "Setting up account..."
                : isInstanceInvite
                  ? "Set password & sign in"
                  : "Create account & join"}
            </Button>

            {!isInstanceInvite ? (
              <p className="text-center text-xs text-muted-foreground">
                Already have an account?{" "}
                <Link
                  to="/login"
                  search={{ returnTo: loginReturnTo }}
                  className="font-medium text-accent hover:underline"
                >
                  Sign in to accept
                </Link>
              </p>
            ) : null}
          </form>
        )}
      </div>
    </main>
  );
}
