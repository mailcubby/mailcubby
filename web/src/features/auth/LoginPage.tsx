import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { AlertCircle } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { BrandMark } from "@/components/BrandMark";
import { PublicFooter } from "@/components/MadeBy";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/features/auth/PasswordInput";
import { ApiError, api, sanitizeReturnTo, unwrap } from "@/lib/api";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";

export interface LoginPageProps {
  returnTo: string | undefined;
}

function formatRateLimitMessage(retryAfterSeconds: number | undefined): string {
  const minutes = retryAfterSeconds ? Math.max(1, Math.ceil(retryAfterSeconds / 60)) : 15;
  return `Too many attempts, try again in ${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}

export function LoginPage({ returnTo }: LoginPageProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const emailRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  const instanceName = meta?.instance_name || "Mailcubby";

  useEffect(() => {
    document.title = `Sign in · ${instanceName}`;
  }, [instanceName]);

  const loginMutation = useMutation({
    mutationFn: async (vars: { email: string; password: string }) =>
      unwrap(
        await api.POST("/api/v1/auth/login", {
          body: vars,
        }),
      ),
    onSuccess: async () => {
      setErrorMessage(null);
      await queryClient.fetchQuery(authMeQueryOptions());
      const target = sanitizeReturnTo(returnTo);
      if (target.includes("?")) {
        window.location.assign(target);
      } else {
        await navigate({ to: target });
      }
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.status === 429) {
          setErrorMessage(formatRateLimitMessage(err.retryAfterSeconds));
          return;
        }
        if (err.status === 401) {
          setErrorMessage("Email or password is incorrect");
          passwordRef.current?.focus();
          return;
        }
        setErrorMessage(err.message);
        return;
      }
      setErrorMessage("Unable to sign in. Please try again.");
    },
  });

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const trimmedEmail = email.trim();
    if (!trimmedEmail) {
      setErrorMessage("Enter your email address.");
      emailRef.current?.focus();
      return;
    }
    if (!password) {
      setErrorMessage("Enter your password.");
      passwordRef.current?.focus();
      return;
    }
    setErrorMessage(null);
    loginMutation.mutate({ email: trimmedEmail, password });
  };

  return (
    <main className="flex min-h-full flex-col items-center justify-center gap-4 px-4 py-10">
      <div className="w-full max-w-sm rounded-lg border border-border bg-surface p-6 shadow-xs">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <BrandMark />
          <div>
            <h1 className="text-base font-semibold tracking-tight text-foreground">
              {instanceName}
            </h1>
            <p className="mt-0.5 text-xs text-muted-foreground">Sign in to your account</p>
          </div>
        </div>

        <form onSubmit={handleSubmit} method="post" className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="email" className="text-xs font-medium text-foreground">
              Email
            </label>
            <Input
              ref={emailRef}
              id="email"
              name="email"
              type="email"
              autoComplete="username"
              spellCheck={false}
              enterKeyHint="next"
              required
              autoFocus
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                if (errorMessage) {
                  setErrorMessage(null);
                }
              }}
              aria-invalid={errorMessage ? "true" : undefined}
              placeholder="you@example.com"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label htmlFor="password" className="text-xs font-medium text-foreground">
              Password
            </label>
            <PasswordInput
              ref={passwordRef}
              id="password"
              name="password"
              autoComplete="current-password"
              enterKeyHint="done"
              required
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                if (errorMessage) {
                  setErrorMessage(null);
                }
              }}
              aria-invalid={errorMessage ? "true" : undefined}
            />
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
            disabled={loginMutation.isPending}
            className="mt-1 w-full"
          >
            {loginMutation.isPending ? "Signing in..." : "Sign in"}
          </Button>
        </form>
      </div>
      {/* Audit V6: the instance name is the title; the product name stays as a quiet footer. */}
      <PublicFooter poweredBy={instanceName !== "Mailcubby"} />
    </main>
  );
}
