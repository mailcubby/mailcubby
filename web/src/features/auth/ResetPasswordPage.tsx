import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertCircle, CheckCircle2 } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { BrandMark } from "@/components/BrandMark";
import { PublicFooter } from "@/components/MadeBy";
import { Button, buttonClassName } from "@/components/ui/button";
import { PasswordInput } from "@/features/auth/PasswordInput";
import { ApiError, api, unwrapVoid } from "@/lib/api";
import { metaQueryOptions } from "@/lib/queries";

export interface ResetPasswordPageProps {
  token: string;
}

export function ResetPasswordPage({ token }: ResetPasswordPageProps) {
  const { data: meta } = useQuery(metaQueryOptions());
  const [password, setPassword] = useState("");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [completed, setCompleted] = useState(false);
  const passwordRef = useRef<HTMLInputElement>(null);

  const instanceName = meta?.instance_name || "Mailcubby";

  useEffect(() => {
    document.title = `Reset password · ${instanceName}`;
  }, [instanceName]);

  const resetMutation = useMutation({
    mutationFn: async (newPassword: string) =>
      unwrapVoid(
        await api.POST("/api/v1/password-reset/{token}", {
          params: { path: { token } },
          body: { password: newPassword },
        }),
      ),
    onSuccess: () => {
      setErrorMessage(null);
      setCompleted(true);
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.status === 410) {
          setErrorMessage(
            "This password reset link is invalid or has expired. Ask an administrator for a new link.",
          );
          return;
        }
        if (err.status === 429) {
          const mins = err.retryAfterSeconds
            ? Math.max(1, Math.ceil(err.retryAfterSeconds / 60))
            : 15;
          setErrorMessage(
            `Too many attempts, try again in ${mins} ${mins === 1 ? "minute" : "minutes"}.`,
          );
          return;
        }
        setErrorMessage(err.details?.password ?? err.message);
        return;
      }
      setErrorMessage("Unable to reset password. Please try again.");
    },
  });

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (password.length < 10 || password.length > 256) {
      setErrorMessage("Password must be between 10 and 256 characters.");
      passwordRef.current?.focus();
      return;
    }
    setErrorMessage(null);
    resetMutation.mutate(password);
  };

  return (
    <main className="flex min-h-full flex-col items-center justify-center gap-4 px-4 py-10">
      <div className="w-full max-w-sm rounded-lg border border-border bg-surface p-6 shadow-xs">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <BrandMark />
          <div>
            <h1 className="text-base font-semibold tracking-tight text-foreground">
              Set a new password
            </h1>
            <p className="mt-0.5 text-xs text-muted-foreground">{instanceName}</p>
          </div>
        </div>

        {completed ? (
          <div className="flex flex-col items-center gap-4 text-center">
            <div className="flex items-center gap-2 rounded-md border border-success/30 bg-success-subtle px-3 py-2 text-xs text-success-foreground">
              <CheckCircle2 className="size-4 shrink-0" aria-hidden="true" />
              <span>Your password has been updated and all previous sessions were signed out.</span>
            </div>
            <Link
              to="/login"
              search={{ returnTo: undefined }}
              className={buttonClassName({ variant: "primary", className: "w-full" })}
            >
              Sign in
            </Link>
          </div>
        ) : (
          <form onSubmit={handleSubmit} method="post" className="flex flex-col gap-4" noValidate>
            <div className="flex flex-col gap-1.5">
              <label htmlFor="new-password" className="text-xs font-medium text-foreground">
                New password
              </label>
              <PasswordInput
                ref={passwordRef}
                id="new-password"
                name="new-password"
                autoComplete="new-password"
                enterKeyHint="done"
                minLength={10}
                maxLength={256}
                required
                autoFocus
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  if (errorMessage) {
                    setErrorMessage(null);
                  }
                }}
                aria-describedby="reset-password-hint"
                aria-invalid={errorMessage ? "true" : undefined}
              />
              <p id="reset-password-hint" className="text-[11px] text-muted-foreground">
                Minimum 10 characters. Existing sessions will be signed out.
              </p>
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
              type="submit"
              variant="primary"
              disabled={resetMutation.isPending}
              className="w-full"
            >
              {resetMutation.isPending ? "Updating password..." : "Reset password"}
            </Button>
          </form>
        )}
      </div>
      <PublicFooter />
    </main>
  );
}
