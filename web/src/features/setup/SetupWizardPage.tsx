import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import {
  AlertCircle,
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  Copy,
  Eye,
  EyeOff,
  Loader2,
  Send,
} from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { BrandMark } from "@/components/BrandMark";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/features/auth/PasswordInput";
import { IntegrationSnippetsPanel } from "@/features/settings/IntegrationSnippetsPanel";
import {
  ApiError,
  api,
  getApiErrorMessage,
  handleApiError,
  type ProjectDetail,
  type SetupRequest,
  type SMTPCredential,
  unwrap,
} from "@/lib/api";
import { copyToClipboard } from "@/lib/clipboard";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

type WizardStep = 1 | 2 | 3 | 4 | 5;

const STEPS: ReadonlyArray<{ num: WizardStep; label: string }> = [
  { num: 1, label: "Token" },
  { num: 2, label: "Admin" },
  { num: 3, label: "Instance" },
  { num: 4, label: "Project" },
  { num: 5, label: "Connect" },
];

// Ways to find the setup token, shown on step 1 (FR-040: the latest token in the log wins).
const SETUP_TOKEN_COMMANDS: ReadonlyArray<{ label: string; command: string }> = [
  {
    label: "Docker Compose",
    command:
      'docker compose -f deploy/compose.server.yml logs mailcubby | grep "setup token" | tail -1',
  },
  {
    label: "Docker",
    command: 'docker logs <container> 2>&1 | grep "setup token" | tail -1',
  },
];

interface ReceivedFirstMessage {
  id: string;
  subject: string;
  from: string;
}

function slugifyProjectName(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

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
  const currentPathname = useRouterState({ select: (s) => s.location.pathname });
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());

  const [step, setStep] = useState<WizardStep>(1);
  const [setupToken, setSetupToken] = useState("");
  // The step 1 error stays hidden until the user tries to continue.
  const [tokenSubmitAttempted, setTokenSubmitAttempted] = useState(false);
  const [adminName, setAdminName] = useState("");
  const [adminEmail, setAdminEmail] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const [instanceName, setInstanceName] = useState("");
  const [instanceTouched, setInstanceTouched] = useState(false);

  // Step 4 state (first project + initial SMTP credential)
  const [projectName, setProjectName] = useState("");
  const [projectSlug, setProjectSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [projectError, setProjectError] = useState<string | null>(null);

  // Step 5 state (connect app + live wait)
  const [createdOnboarding, setCreatedOnboarding] = useState<{
    project: ProjectDetail;
    credential: SMTPCredential;
    password: string;
  } | null>(null);
  const [passwordRevealed, setPasswordRevealed] = useState(false);
  const [firstMessage, setFirstMessage] = useState<ReceivedFirstMessage | null>(null);

  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const stepHeadingRef = useRef<HTMLHeadingElement>(null);
  const tokenInputRef = useRef<HTMLInputElement>(null);
  const nameInputRef = useRef<HTMLInputElement>(null);
  const emailInputRef = useRef<HTMLInputElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const instanceInputRef = useRef<HTMLInputElement>(null);
  const projectNameInputRef = useRef<HTMLInputElement>(null);

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
    setProjectError(null);
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
      moveStep(4);
      // Fetch /auth/me first to populate the in-memory CSRF token for steps 4 & 5.
      await queryClient.fetchQuery(authMeQueryOptions());
      await queryClient.invalidateQueries({ queryKey: queryKeys.meta() });
      toast.success("Admin account created");
    },
    onError: async (err) => {
      if (err instanceof ApiError) {
        if (err.status === 403) {
          // UI 3.2: a wrong token sends the user back to step 1 with the error.
          setFieldErrors({});
          setFormError("Invalid setup token. Check the server logs and paste the latest token.");
          setTokenSubmitAttempted(true);
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

  // Redirect direct visits to /setup once setup is already complete (steps 4-5 stay on /setup).
  useEffect(() => {
    if (
      currentPathname === "/setup" &&
      meta &&
      !meta.setup_required &&
      step <= 3 &&
      !setupMutation.isPending
    ) {
      void navigate({ to: "/", replace: true });
    }
  }, [currentPathname, meta, step, setupMutation.isPending, navigate]);

  const createProjectMutation = useMutation({
    mutationFn: async (vars: { name: string; slug: string }) => {
      const project = unwrap(
        await api.POST("/api/v1/projects", {
          body: {
            name: vars.name,
            ...(vars.slug ? { slug: vars.slug } : {}),
          },
        }),
      );
      const cred = unwrap(
        await api.POST("/api/v1/projects/{project}/smtp-credentials", {
          params: { path: { project: project.slug } },
          body: { label: "default" },
        }),
      );
      return {
        project,
        credential: cred.credential,
        password: cred.password,
      };
    },
    onSuccess: async (result) => {
      setProjectError(null);
      setCreatedOnboarding(result);
      setFirstMessage(null);
      setPasswordRevealed(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.projects() }),
        queryClient.invalidateQueries({ queryKey: queryKeys.authMe() }),
      ]);
      toast.success(`Project "${result.project.name}" created`);
      moveStep(5);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.details) {
        const first = Object.values(err.details)[0];
        if (first) {
          setProjectError(first);
          return;
        }
      }
      setProjectError(getApiErrorMessage(err, "Failed to create project"));
    },
  });

  const sendTestMutation = useMutation({
    mutationFn: async (slug: string) =>
      unwrap(
        await api.POST("/api/v1/projects/{project}/send-test", {
          params: { path: { project: slug } },
        }),
      ),
    onSuccess: () => {
      toast.success("Test email sent");
    },
    onError: (err) => {
      handleApiError(err, "Failed to send test email");
    },
  });

  // Step 5: subscribe to live SSE events for the created project until the first message arrives.
  useEffect(() => {
    if (step !== 5 || !createdOnboarding) {
      return;
    }
    const slug = createdOnboarding.project.slug;
    const source = new EventSource(`/api/v1/events?project=${encodeURIComponent(slug)}`);

    const handleMessageCreated = (ev: MessageEvent) => {
      try {
        const parsed = JSON.parse(String(ev.data)) as {
          message?: {
            id?: string;
            subject?: string;
            from?: { name?: string; address?: string };
          };
        };
        const msg = parsed.message;
        if (msg?.id) {
          setFirstMessage({
            id: msg.id,
            subject: msg.subject || "(no subject)",
            from: msg.from?.name || msg.from?.address || "Unknown sender",
          });
        }
      } catch {
        // Ignore malformed payloads
      }
    };

    source.addEventListener("message.created", handleMessageCreated as EventListener);
    return () => {
      source.removeEventListener("message.created", handleMessageCreated as EventListener);
      source.close();
    };
  }, [step, createdOnboarding]);

  const handleStep1Submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setTokenSubmitAttempted(true);
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

  const handleStep4Submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const trimmedName = projectName.trim();
    const trimmedSlug = projectSlug.trim();
    if (trimmedName.length < 1 || trimmedName.length > 80) {
      setProjectError("Project name must be between 1 and 80 characters.");
      projectNameInputRef.current?.focus();
      return;
    }
    setProjectError(null);
    createProjectMutation.mutate({ name: trimmedName, slug: trimmedSlug });
  };

  if (meta && !meta.setup_required && step <= 3 && !setupMutation.isPending) {
    return null;
  }

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
      <div
        className={cn(
          "w-full rounded-lg border border-border bg-surface p-6 shadow-xs",
          step >= 5 ? "max-w-2xl" : "max-w-lg",
        )}
      >
        <div className="mb-5 flex items-center gap-2.5">
          <BrandMark />
          <div>
            <h1 className="text-base font-semibold tracking-tight text-foreground">
              Set up {meta?.instance_name || "Mailcubby"}
            </h1>
            <p className="text-xs text-muted-foreground">First-run configuration for server mode</p>
          </div>
        </div>

        <nav aria-label="Setup progress" className="mb-6">
          <ol className="grid grid-cols-5 gap-1.5">
            {STEPS.map((item) => {
              const done = step > item.num;
              const current = step === item.num;
              return (
                <li
                  key={item.num}
                  aria-current={current ? "step" : undefined}
                  className={cn(
                    "flex min-w-0 items-center justify-center gap-1.5 rounded-md border px-1.5 py-1.5 text-xs font-medium sm:justify-start",
                    current
                      ? "border-accent bg-accent-subtle text-accent-subtle-foreground"
                      : done
                        ? "border-border bg-surface text-foreground"
                        : "border-border bg-background text-muted-foreground",
                  )}
                >
                  <span
                    className={cn(
                      "flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold tabular-nums",
                      current
                        ? "bg-primary text-primary-foreground"
                        : done
                          ? "bg-success-subtle text-success"
                          : "border border-input text-muted-foreground",
                    )}
                  >
                    {done ? <Check className="size-3" aria-hidden="true" /> : item.num}
                  </span>
                  <span className="hidden whitespace-nowrap sm:inline">
                    {item.label}
                    {done ? <span className="sr-only"> (done)</span> : null}
                  </span>
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
                server generates a new token, so use the latest one.
              </p>
              <ul aria-label="Where to find the token" className="mt-2 flex flex-col gap-1.5">
                {SETUP_TOKEN_COMMANDS.map((item) => (
                  <li key={item.label} className="flex flex-col gap-0.5">
                    <span className="text-[11px] font-medium text-muted-foreground">
                      {item.label}
                    </span>
                    <div className="flex items-center gap-1">
                      <pre
                        // biome-ignore lint/a11y/noNoninteractiveTabindex: scrollable code region needs keyboard focus (WCAG 2.1.1).
                        tabIndex={0}
                        className="min-w-0 flex-1 overflow-x-auto rounded-md border border-border bg-background px-3 py-1.5 font-mono text-[11px] text-foreground"
                      >
                        {item.command}
                      </pre>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Copy ${item.label} command`}
                        onClick={() => void copyToClipboard(item.command, `${item.label} command`)}
                        className="size-7 shrink-0"
                      >
                        <Copy className="size-3" aria-hidden="true" />
                      </Button>
                    </div>
                  </li>
                ))}
                <li className="flex flex-col gap-0.5">
                  <span className="text-[11px] font-medium text-muted-foreground">Binary</span>
                  <span className="text-xs text-foreground">
                    Look for the <code className="font-mono text-[11px]">setup token:</code> line in
                    the terminal or log where{" "}
                    <code className="font-mono text-[11px]">mailcubby serve</code> runs.
                  </span>
                </li>
              </ul>
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
                aria-invalid={tokenSubmitAttempted && formError ? "true" : undefined}
                aria-describedby={
                  tokenSubmitAttempted && formError ? "setup-token-error" : undefined
                }
                placeholder="Paste token from server logs"
                className="font-mono"
              />
            </div>

            {tokenSubmitAttempted && formError ? (
              <div
                id="setup-token-error"
                role="alert"
                className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive-subtle px-3 py-2 text-xs text-destructive"
              >
                <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                <span>{formError}</span>
              </div>
            ) : null}

            <div className="flex justify-end pt-1">
              <Button type="submit" variant="primary">
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
                      ? "text-success"
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
              <Button type="submit" variant="primary">
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
                enterKeyHint="next"
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
                    <div className="mt-0.5 text-[11px] text-warning">
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
                    <div className="mt-0.5 text-[11px] text-warning">
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
                className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive-subtle px-3 py-2 text-xs text-destructive"
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
              <Button type="submit" variant="primary" disabled={setupMutation.isPending}>
                {setupMutation.isPending ? "Completing setup..." : "Complete setup"}
              </Button>
            </div>
          </form>
        ) : null}

        {step === 4 ? (
          <form onSubmit={handleStep4Submit} className="flex flex-col gap-4" noValidate>
            <div>
              <h2
                ref={stepHeadingRef}
                tabIndex={-1}
                className="text-sm font-semibold text-foreground outline-none"
              >
                Step 4: First project
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Create your first project inbox and generate its initial SMTP credential.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="first-project-name" className="text-xs font-medium text-foreground">
                Project name
              </label>
              <Input
                ref={projectNameInputRef}
                id="first-project-name"
                name="project_name"
                type="text"
                autoFocus
                required
                maxLength={80}
                placeholder="Shop Staging"
                value={projectName}
                onChange={(e) => {
                  const next = e.target.value;
                  setProjectName(next);
                  if (!slugTouched) {
                    setProjectSlug(slugifyProjectName(next));
                  }
                  if (projectError) setProjectError(null);
                }}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="first-project-slug" className="text-xs font-medium text-foreground">
                Project slug
              </label>
              <Input
                id="first-project-slug"
                name="project_slug"
                type="text"
                maxLength={63}
                placeholder="shop-staging"
                className="font-mono"
                value={projectSlug}
                onChange={(e) => {
                  setSlugTouched(true);
                  setProjectSlug(e.target.value);
                  if (projectError) setProjectError(null);
                }}
              />
              <p className="text-[11px] text-muted-foreground">
                Used in URLs, SMTP usernames, and recipient routing addresses.
              </p>
            </div>

            {projectError ? (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive-subtle px-3 py-2 text-xs text-destructive"
              >
                <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                <span>{projectError}</span>
              </div>
            ) : null}

            <div className="flex items-center justify-between pt-1">
              <Button
                type="button"
                variant="ghost"
                disabled={createProjectMutation.isPending}
                onClick={() => void navigate({ to: "/" })}
              >
                Skip for now
              </Button>
              <Button type="submit" variant="primary" disabled={createProjectMutation.isPending}>
                <span>
                  {createProjectMutation.isPending
                    ? "Creating project..."
                    : "Create project & continue"}
                </span>
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </form>
        ) : null}

        {step === 5 && createdOnboarding ? (
          <div className="flex flex-col gap-4">
            <div>
              <h2
                ref={stepHeadingRef}
                tabIndex={-1}
                className="text-sm font-semibold text-foreground outline-none"
              >
                Step 5: Connect your app
              </h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Configure your application with the SMTP credentials for{" "}
                <span className="font-medium text-foreground">
                  {createdOnboarding.project.name}
                </span>{" "}
                or send a built-in test email to verify delivery.
              </p>
            </div>

            <div
              role="status"
              aria-live="polite"
              // Audit M9: waiting is neutral; the arrival is the one success moment.
              className={cn(
                "flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3",
                firstMessage
                  ? "border-success/30 bg-success-subtle text-success-foreground"
                  : "border-border bg-background text-foreground",
              )}
            >
              <div className="flex min-w-0 items-center gap-3">
                <span
                  aria-hidden="true"
                  className={cn(
                    "flex size-9 shrink-0 items-center justify-center rounded-full",
                    firstMessage ? "bg-surface text-success" : "bg-secondary text-muted-foreground",
                  )}
                >
                  {firstMessage ? (
                    <CheckCircle2 className="size-5" />
                  ) : (
                    <Loader2 className="size-4 animate-spin motion-reduce:animate-none" />
                  )}
                </span>
                <div className="min-w-0">
                  {firstMessage ? (
                    <>
                      <div className="text-[13px] font-semibold">First message received!</div>
                      <div className="truncate text-xs">
                        {firstMessage.subject} · from {firstMessage.from}
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="text-[13px] font-medium">Waiting for first message...</div>
                      <div className="text-xs text-muted-foreground">
                        Send an email via SMTP or click &quot;Send test email&quot; to test right
                        now.
                      </div>
                    </>
                  )}
                </div>
              </div>

              <Button
                type="button"
                variant={firstMessage ? "outline" : "primary"}
                size="sm"
                disabled={sendTestMutation.isPending}
                onClick={() => sendTestMutation.mutate(createdOnboarding.project.slug)}
              >
                <Send className="size-3.5" aria-hidden="true" />
                <span>{sendTestMutation.isPending ? "Sending..." : "Send test email"}</span>
              </Button>
            </div>

            <section className="rounded-md border border-border bg-background text-xs">
              <dl className="grid grid-cols-1 divide-y divide-border sm:grid-cols-2 sm:divide-y-0">
                {[
                  { label: "Host", value: meta?.smtp.host ?? "localhost" },
                  { label: "Port", value: String(meta?.smtp.port ?? 587) },
                  { label: "TLS", value: tlsMode },
                  { label: "Username", value: createdOnboarding.credential.username },
                ].map((row) => (
                  <div
                    key={row.label}
                    className="flex items-center justify-between gap-2 border-b border-border px-3 py-2 sm:odd:border-r"
                  >
                    <dt className="text-muted-foreground">{row.label}</dt>
                    <dd className="flex min-w-0 items-center gap-1 font-mono text-foreground">
                      <span className="truncate">{row.value}</span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={`Copy ${row.label}`}
                        onClick={() => void copyToClipboard(row.value, row.label)}
                        className="size-6 shrink-0"
                      >
                        <Copy className="size-3" aria-hidden="true" />
                      </Button>
                    </dd>
                  </div>
                ))}
                <div className="flex items-center justify-between gap-2 px-3 py-2 sm:col-span-2">
                  <dt className="text-muted-foreground">Password</dt>
                  <dd className="flex min-w-0 items-center gap-1 font-mono text-foreground">
                    <span className="truncate">
                      {passwordRevealed ? createdOnboarding.password : "••••••••••••"}
                    </span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={passwordRevealed ? "Hide password" : "Reveal password"}
                      onClick={() => setPasswordRevealed((prev) => !prev)}
                      className="size-6 shrink-0"
                    >
                      {passwordRevealed ? (
                        <EyeOff className="size-3" aria-hidden="true" />
                      ) : (
                        <Eye className="size-3" aria-hidden="true" />
                      )}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label="Copy Password"
                      onClick={() =>
                        void copyToClipboard(createdOnboarding.password, "SMTP password")
                      }
                      className="size-6 shrink-0"
                    >
                      <Copy className="size-3" aria-hidden="true" />
                    </Button>
                  </dd>
                </div>
              </dl>
            </section>

            <IntegrationSnippetsPanel
              input={{
                mode: "server",
                host: meta?.smtp.host ?? "localhost",
                port: meta?.smtp.port ?? 587,
                tlsMode,
                username: createdOnboarding.credential.username,
                password: passwordRevealed ? createdOnboarding.password : "********",
              }}
              passwordRevealed={passwordRevealed}
              canRevealPassword
              onToggleRevealPassword={() => setPasswordRevealed((prev) => !prev)}
            />

            <div className="flex items-center justify-end pt-1">
              <Button
                type="button"
                variant={firstMessage ? "primary" : "outline"}
                onClick={() =>
                  void navigate({
                    to: "/p/$project",
                    params: { project: createdOnboarding.project.slug },
                  })
                }
              >
                <span>Go to inbox</span>
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </div>
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
          ? "border-transparent bg-success-subtle text-success-foreground"
          : "border-transparent bg-warning-subtle text-warning-foreground",
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
