import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  Inbox,
} from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/features/auth/PasswordInput";
import { ApiError, api, type SetupRequest, unwrap } from "@/lib/api";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

type WizardStep = 1 | 2 | 3;

const STEPS: ReadonlyArray<{ num: WizardStep; label: string }> = [
  { num: 1, label: "Setup token" },
  { num: 2, label: "Admin account" },
  { num: 3, label: "Instance" },
];

function passwordStrengthHint(password: string): { label: string; ok: boolean } {
  if (password.length === 0) {
    return { label: "Minimum 10 characters", ok: false };
  }
  if (password.length < 10) {
    return {
      label: `Too short (${password.length}/10 characters minimum)`,
      ok: false,
    };
  }
  let variety = 0;
  if (/[a-z]/.test(password)) variety += 1;
  if (/[A-Z]/.test(password)) variety += 1;
  if (/[0-9]/.test(password)) variety += 1;
  if (/[^a-zA-Z0-9]/.test(password)) variety += 1;
  if (password.length >= 16 && variety >= 3) {
    return { label: "Strong password", ok: true };
  }
  if (password.length >= 12 && variety >= 2) {
    return { label: "Good password", ok: true };
  }
  return { label: "Meets minimum length — consider a longer passphrase", ok: true };
}

export function SetupWizardPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());

  const [step, setStep] = useState<WizardStep>(1);
  const [setupToken, setSetupToken] = useState("");
  const [adminName, setAdminName] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const [instanceName, setInstanceName] = useState("");
  const [instanceTouched, setInstanceTouched] = useState(false);

  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const stepHeadingRef = useRef<HTMLHeadingElement>(null);
  const tokenInputRef = useRef<HTMLInputElement>(null);
  const nameInputRef = useRef<HTMLInputElement>(null);
  const emailInputRef = useRef<HTMLInputElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const instanceInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!instanceTouched && meta?.instance_name) {
      setInstanceName(meta.instance_name);
    }
  }, [instanceTouched, meta?.instance_name]);

  useEffect(() => {
    document.title = `Setup · ${meta?.instance_name || "Mailcubby"}`;
  }, [meta?.instance_name]);

  const moveStep = (next: WizardStep) => {
    setFormError(null);
    setStep(next);
    requestAnimationFrame(() => {
      stepHeadingRef.current?.focus();
    });
  };

  const setupMutation = useMutation({
    mutationFn: async (body: SetupRequest) =>
      unwrap(
        await api.POST("/api/v1/setup", {
          body,
        }),
      ),
    onSuccess: async () => {
      setFormError(null);
      setFieldErrors({});
      await queryClient.invalidateQueries({ queryKey: queryKeys.meta() });
      await queryClient.fetchQuery(authMeQueryOptions());
      toast.success("Mailcubby setup complete");
      await navigate({ to: "/" });
    },
    onError: async (err) => {
      if (err instanceof ApiError) {
        if (err.status === 403) {
          // UI 3.2: a wrong token sends the user back to step 1 with the error.
          setFieldErrors({});
          setFormError("Invalid setup token. Check the server logs and paste the latest token.");
          setStep(1);
          requestAnimationFrame(() => {
            tokenInputRef.current?.focus();
          });
          return;
        }
        if (err.status === 409) {
          await queryClient.invalidateQueries({ queryKey: queryKeys.meta() });
          toast.info("Setup has already been completed. Sign in to continue.");
          await navigate({ to: "/login", search: { returnTo: undefined } });
          return;
        }
        if (err.status === 422 && err.details) {
          setFieldErrors(err.details);
          if (err.details.name || err.details.email || err.details.password) {
            setStep(2);
            requestAnimationFrame(() => {
              if (err.details?.name) {
                nameInputRef.current?.focus();
              } else if (err.details?.email) {
                emailInputRef.current?.focus();
              } else {
                passwordInputRef.current?.focus();
              }
            });
            return;
          }
          if (err.details.instance_name) {
            setStep(3);
            requestAnimationFrame(() => {
              instanceInputRef.current?.focus();
            });
            return;
          }
        }
        setFormError(err.message);
        return;
      }
      setFormError("Failed to complete setup. Please try again.");
    },
  });

  const handleStep1Submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!setupToken.trim()) {
      setFormError("Paste the setup token from your Mailcubby server logs.");
      tokenInputRef.current?.focus();
      return;
    }
    moveStep(2);
  };

  const handleStep2Submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    const trimmedName = adminName.trim();
    const trimmedEmail = adminEmail.trim();

    if (trimmedName.length < 1 || trimmedName.length > 80) {
      errs.name = "Name must be between 1 and 80 characters.";
    }
    if (!trimmedEmail.includes("@") || !trimmedEmail.includes(".")) {
      errs.email = "Enter a valid email address.";
    }
    if (adminPassword.length < 10 || adminPassword.length > 256) {
      errs.password = "Password must be between 10 and 256 characters.";
    }

    if (Object.keys(errs).length > 0) {
      setFieldErrors(errs);
      if (errs.name) {
        nameInputRef.current?.focus();
      } else if (errs.email) {
        emailInputRef.current?.focus();
      } else {
        passwordInputRef.current?.focus();
      }
      return;
    }
    setFieldErrors({});
    moveStep(3);
  };

  const handleStep3Submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const trimmedInstance = instanceName.trim();
    if (trimmedInstance.length > 80) {
      setFieldErrors({ instance_name: "Instance name must be at most 80 characters." });
      instanceInputRef.current?.focus();
      return;
    }
    setFieldErrors({});
    setFormError(null);

    const payload: SetupRequest = {
      setup_token: setupToken.trim(),
      name: adminName.trim(),
      email: adminEmail.trim(),
      password: adminPassword,
      ...(trimmedInstance ? { instance_name: trimmedInstance } : {}),
    };
    setupMutation.mutate(payload);
  };

  const currentOrigin = typeof window !== "undefined" ? window.location.origin : "";
  const configuredBaseURL = meta?.base_url ?? "";
  const originMatches =
    Boolean(configuredBaseURL) &&
    Boolean(currentOrigin) &&
    configuredBaseURL.replace(/\/+$/, "").toLowerCase() === currentOrigin.toLowerCase();
  const tlsMode = meta?.smtp.tls_mode ?? "none";
  const tlsEnabled = tlsMode === "starttls" || tlsMode === "tls";
  const strength = passwordStrengthHint(adminPassword);

  return (
    <main className="flex min-h-full items-center justify-center px-4 py-10">
      <div className="w-full max-w-lg rounded-lg border border-border bg-surface p-6 shadow-xs">
        <div className="mb-5 flex items-center gap-2.5">
          <span className="flex size-8 items-center justify-center rounded-md bg-foreground text-background">
            <Inbox className="size-4" aria-hidden="true" />
          </span>
          <div>
            <h1 className="text-base font-semibold tracking-tight text-foreground">
              Set up Mailcubby
            </h1>
            <p className="text-xs text-muted-foreground">First-run configuration for server mode</p>
          </div>
        </div>

        <nav aria-label="Setup progress" className="mb-6">
          <ol className="grid grid-cols-3 gap-2">
            {STEPS.map((item) => {
              const done = step > item.num;
              const current = step === item.num;
              return (
                <li
                  key={item.num}
                  aria-current={current ? "step" : undefined}
                  className={cn(
                    "flex items-center gap-2 rounded-md border px-2.5 py-2 text-xs font-medium",
                    current
                      ? "border-accent bg-accent/10 text-foreground"
                      : done
                        ? "border-border bg-background text-foreground"
                        : "border-border/70 bg-background/50 text-muted-foreground",
                  )}
                >
                  <span
                    className={cn(
                      "flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold tabular-nums",
                      current
                        ? "bg-accent text-accent-foreground"
                        : done
                          ? "bg-foreground text-background"
                          : "border border-border text-muted-foreground",
                    )}
                  >
                    {done ? <Check className="size-3" aria-hidden="true" /> : item.num}
                  </span>
                  <span className="truncate">{item.label}</span>
                </li>
              );
            })}
          </ol>
        </nav>

        {step === 1 ? (
          <form onSubmit={handleStep1Submit} className="flex flex-col gap-4" noValidate>
            <div>
              <h2
                ref={stepHeadingRef}
                tabIndex={-1}
                className="text-sm font-semibold text-foreground outline-none"
              >
                Step 1: Setup token
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Mailcubby prints a one-time setup token to its logs on startup. Restarting the
                server generates a new token.
              </p>
              <pre className="mt-2 overflow-x-auto rounded-md border border-border bg-background px-3 py-2 font-mono text-[11px] text-foreground">
                docker compose logs mailcubby | grep &quot;setup token&quot;
              </pre>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="setup-token" className="text-xs font-medium text-foreground">
                Setup token
              </label>
              <Input
                ref={tokenInputRef}
                id="setup-token"
                name="setup_token"
                type="text"
                autoComplete="off"
                spellCheck={false}
                enterKeyHint="next"
                required
                autoFocus
                value={setupToken}
                onChange={(e) => {
                  setSetupToken(e.target.value);
                  if (formError) {
                    setFormError(null);
                  }
                }}
                aria-invalid={formError ? "true" : undefined}
                placeholder="Paste token from server logs"
                className="font-mono"
              />
            </div>

            {formError ? (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
              >
                <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                <span>{formError}</span>
              </div>
            ) : null}

            <div className="flex justify-end pt-1">
              <Button type="submit">
                <span>Continue to admin account</span>
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </form>
        ) : null}

        {step === 2 ? (
          <form onSubmit={handleStep2Submit} className="flex flex-col gap-4" noValidate>
            <div>
              <h2
                ref={stepHeadingRef}
                tabIndex={-1}
                className="text-sm font-semibold text-foreground outline-none"
              >
                Step 2: Admin account
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Create the initial instance administrator. You can invite teammates and clients
                after setup.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="admin-name" className="text-xs font-medium text-foreground">
                Full name
              </label>
              <Input
                ref={nameInputRef}
                id="admin-name"
                name="name"
                type="text"
                autoComplete="name"
                enterKeyHint="next"
                required
                maxLength={80}
                value={adminName}
                onChange={(e) => {
                  setAdminName(e.target.value);
                  if (fieldErrors.name) {
                    setFieldErrors((prev) => {
                      const next = { ...prev };
                      delete next.name;
                      return next;
                    });
                  }
                }}
                aria-invalid={fieldErrors.name ? "true" : undefined}
                aria-describedby={fieldErrors.name ? "admin-name-error" : undefined}
                placeholder="Alex Rivera"
              />
              {fieldErrors.name ? (
                <p id="admin-name-error" className="text-xs text-destructive">
                  {fieldErrors.name}
                </p>
              ) : null}
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="admin-email" className="text-xs font-medium text-foreground">
                Email
              </label>
              <Input
                ref={emailInputRef}
                id="admin-email"
                name="email"
                type="email"
                autoComplete="username"
                spellCheck={false}
                enterKeyHint="next"
                required
                value={adminEmail}
                onChange={(e) => {
                  setAdminEmail(e.target.value);
                  if (fieldErrors.email) {
                    setFieldErrors((prev) => {
                      const next = { ...prev };
                      delete next.email;
                      return next;
                    });
                  }
                }}
                aria-invalid={fieldErrors.email ? "true" : undefined}
                aria-describedby={fieldErrors.email ? "admin-email-error" : undefined}
                placeholder="admin@example.com"
              />
              {fieldErrors.email ? (
                <p id="admin-email-error" className="text-xs text-destructive">
                  {fieldErrors.email}
                </p>
              ) : null}
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="admin-password" className="text-xs font-medium text-foreground">
                Password
              </label>
              <PasswordInput
                ref={passwordInputRef}
                id="admin-password"
                name="password"
                autoComplete="new-password"
                enterKeyHint="next"
                minLength={10}
                maxLength={256}
                required
                value={adminPassword}
                onChange={(e) => {
                  setAdminPassword(e.target.value);
                  if (fieldErrors.password) {
                    setFieldErrors((prev) => {
                      const next = { ...prev };
                      delete next.password;
                      return next;
                    });
                  }
                }}
                aria-invalid={fieldErrors.password ? "true" : undefined}
                aria-describedby="admin-password-hint"
              />
              <p
                id="admin-password-hint"
                className={cn(
                  "text-xs",
                  fieldErrors.password
                    ? "text-destructive"
                    : strength.ok
                      ? "text-emerald-700 dark:text-emerald-400"
                      : "text-muted-foreground",
                )}
              >
                {fieldErrors.password ?? strength.label}
              </p>
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button type="button" variant="outline" onClick={() => moveStep(1)}>
                <ArrowLeft className="size-3.5" aria-hidden="true" />
                <span>Back</span>
              </Button>
              <Button type="submit">
                <span>Continue to instance</span>
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </form>
        ) : null}

        {step === 3 ? (
          <form onSubmit={handleStep3Submit} className="flex flex-col gap-4" noValidate>
            <div>
              <h2
                ref={stepHeadingRef}
                tabIndex={-1}
                className="text-sm font-semibold text-foreground outline-none"
              >
                Step 3: Instance
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Name this Mailcubby instance and review the runtime configuration detected by the
                server.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="instance-name" className="text-xs font-medium text-foreground">
                Instance name
              </label>
              <Input
                ref={instanceInputRef}
                id="instance-name"
                name="instance_name"
                type="text"
                enterKeyHint="done"
                maxLength={80}
                value={instanceName}
                onChange={(e) => {
                  setInstanceTouched(true);
                  setInstanceName(e.target.value);
                  if (fieldErrors.instance_name) {
                    setFieldErrors((prev) => {
                      const next = { ...prev };
                      delete next.instance_name;
                      return next;
                    });
                  }
                }}
                aria-invalid={fieldErrors.instance_name ? "true" : undefined}
                placeholder="Mailcubby"
              />
              {fieldErrors.instance_name ? (
                <p className="text-xs text-destructive">{fieldErrors.instance_name}</p>
              ) : null}
            </div>

            <div className="flex flex-col divide-y divide-border rounded-md border border-border bg-background text-xs">
              <div className="flex items-start justify-between gap-3 px-3 py-2.5">
                <div className="min-w-0">
                  <div className="font-medium text-foreground">Public base URL</div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">
                    {configuredBaseURL || "Not configured"}
                  </div>
                  {!originMatches && currentOrigin ? (
                    <div className="mt-0.5 text-[11px] text-amber-700 dark:text-amber-400">
                      Browser origin is {currentOrigin} — Origin checks require these to match.
                    </div>
                  ) : null}
                </div>
                <StatusPill ok={originMatches} label={originMatches ? "Matches" : "Mismatch"} />
              </div>

              <div className="flex items-start justify-between gap-3 px-3 py-2.5">
                <div className="min-w-0">
                  <div className="font-medium text-foreground">SMTP endpoint</div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">
                    {meta ? `${meta.smtp.host}:${meta.smtp.port}` : "Loading..."} · TLS: {tlsMode}
                  </div>
                  {!tlsEnabled ? (
                    <div className="mt-0.5 text-[11px] text-amber-700 dark:text-amber-400">
                      TLS is off (insecure AUTH allowed). Configure a certificate for production.
                    </div>
                  ) : null}
                </div>
                <StatusPill ok={tlsEnabled} label={tlsEnabled ? tlsMode.toUpperCase() : "No TLS"} />
              </div>

              {meta?.inbox_domain ? (
                <div className="flex items-start justify-between gap-3 px-3 py-2.5">
                  <div className="min-w-0">
                    <div className="font-medium text-foreground">Inbox domain</div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">
                      {meta.inbox_domain}
                    </div>
                  </div>
                  <StatusPill ok={true} label="Configured" />
                </div>
              ) : null}
            </div>

            {formError ? (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
              >
                <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                <span>{formError}</span>
              </div>
            ) : null}

            <div className="flex items-center justify-between pt-1">
              <Button
                type="button"
                variant="outline"
                disabled={setupMutation.isPending}
                onClick={() => moveStep(2)}
              >
                <ArrowLeft className="size-3.5" aria-hidden="true" />
                <span>Back</span>
              </Button>
              <Button type="submit" disabled={setupMutation.isPending}>
                {setupMutation.isPending ? "Completing setup..." : "Complete setup"}
              </Button>
            </div>
          </form>
        ) : null}
      </div>
    </main>
  );
}

function StatusPill({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium",
        ok
          ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400"
          : "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
      )}
    >
      {ok ? (
        <CheckCircle2 className="size-3" aria-hidden="true" />
      ) : (
        <AlertTriangle className="size-3" aria-hidden="true" />
      )}
      <span>{label}</span>
    </span>
  );
}
