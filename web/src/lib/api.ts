import createClient from "openapi-fetch";
import { toast } from "sonner";
import type { components, paths } from "@/api/schema";

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

// Stored strictly in memory, never localStorage (SPEC 12.3, AGENTS.md 6).
let csrfToken = "";

export function getCsrfToken(): string {
  return csrfToken;
}

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

export const api = createClient<paths>({
  baseUrl: "",
  credentials: "same-origin",
});

api.use({
  onRequest({ request }) {
    if (UNSAFE_METHODS.has(request.method.toUpperCase()) && csrfToken) {
      request.headers.set("X-CSRF-Token", csrfToken);
    }
    return request;
  },
});

export type ProjectColor = components["schemas"]["ProjectColor"];
export type ProjectRole = components["schemas"]["ProjectRole"];
export type SMTPConnectionInfo = components["schemas"]["SMTPConnectionInfo"];
export type SMTPCredential = components["schemas"]["SMTPCredential"];
export type SMTPCredentialListResponse = components["schemas"]["SMTPCredentialListResponse"];
export type CreateSMTPCredentialRequest = components["schemas"]["CreateSMTPCredentialRequest"];
export type CreateSMTPCredentialResponse = components["schemas"]["CreateSMTPCredentialResponse"];
export type RevealSMTPCredentialResponse = components["schemas"]["RevealSMTPCredentialResponse"];
export type UpdateSMTPCredentialRequest = components["schemas"]["UpdateSMTPCredentialRequest"];
export type MetaResponse = components["schemas"]["MetaResponse"];
export type AuthUser = components["schemas"]["AuthUser"];
export type AuthMeResponse = components["schemas"]["AuthMeResponse"];
export type SetupRequest = components["schemas"]["SetupRequest"];
export type LoginRequest = components["schemas"]["LoginRequest"];
export type PatchAuthMeRequest = components["schemas"]["PatchAuthMeRequest"];
export type SessionSummary = components["schemas"]["SessionSummary"];
export type PasswordResetRequest = components["schemas"]["PasswordResetRequest"];
export type ProjectSummary = components["schemas"]["ProjectSummary"];
export type ProjectDetail = components["schemas"]["ProjectDetail"];
export type ProjectSettings = components["schemas"]["ProjectSettings"];
export type CreateProjectRequest = components["schemas"]["CreateProjectRequest"];
export type UpdateProjectRequest = components["schemas"]["UpdateProjectRequest"];
export type DeleteProjectRequest = components["schemas"]["DeleteProjectRequest"];
export type MemberUser = components["schemas"]["MemberUser"];
export type ProjectMember = components["schemas"]["ProjectMember"];
export type UpdateProjectMemberRequest = components["schemas"]["UpdateProjectMemberRequest"];
export type InviteStatus = components["schemas"]["InviteStatus"];
export type ProjectInvite = components["schemas"]["ProjectInvite"];
export type CreateProjectInviteRequest = components["schemas"]["CreateProjectInviteRequest"];
export type CreateProjectInviteResponse = components["schemas"]["CreateProjectInviteResponse"];
export type InviteProjectPreview = components["schemas"]["InviteProjectPreview"];
export type InvitePreview = components["schemas"]["InvitePreview"];
export type AcceptInviteRequest = components["schemas"]["AcceptInviteRequest"];
export type AcceptInviteResponse = components["schemas"]["AcceptInviteResponse"];
export type AdminUser = components["schemas"]["AdminUser"];
export type AdminUserListResponse = components["schemas"]["AdminUserListResponse"];
export type AdminCreateUserRequest = components["schemas"]["AdminCreateUserRequest"];
export type AdminCreateUserResponse = components["schemas"]["AdminCreateUserResponse"];
export type AdminUpdateUserRequest = components["schemas"]["AdminUpdateUserRequest"];
export type AdminPasswordResetResponse = components["schemas"]["AdminPasswordResetResponse"];
export type AdminProject = components["schemas"]["AdminProject"];
export type Address = components["schemas"]["Address"];
export type Header = components["schemas"]["Header"];
export type AttachmentSummary = components["schemas"]["AttachmentSummary"];
export type MessageEnvelope = components["schemas"]["MessageEnvelope"];
export type MessageSummary = components["schemas"]["MessageSummary"];
export type MessageListResponse = components["schemas"]["MessageListResponse"];
export type MessageDetail = components["schemas"]["MessageDetail"];
export type PatchMessageRequest = components["schemas"]["PatchMessageRequest"];
export type BulkMessagesRequest = components["schemas"]["BulkMessagesRequest"];
export type TagSummary = components["schemas"]["TagSummary"];
export type CreateTagRequest = components["schemas"]["CreateTagRequest"];
export type UpdateTagRequest = components["schemas"]["UpdateTagRequest"];
export type ErrorEnvelope = components["schemas"]["ErrorEnvelope"];

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, string> | undefined;
  readonly retryAfterSeconds: number | undefined;

  constructor(
    status: number,
    code: string,
    message: string,
    details?: Record<string, string>,
    retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

function parseRetryAfterSeconds(
  response: Response,
  details?: Record<string, string>,
): number | undefined {
  const fromHeader = response.headers.get("Retry-After");
  const candidate = fromHeader ?? details?.retry_after;
  if (!candidate) {
    return undefined;
  }
  const parsed = Number.parseInt(candidate, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

function toApiError(response: Response, error: unknown): ApiError {
  if (
    error &&
    typeof error === "object" &&
    "error" in error &&
    error.error &&
    typeof error.error === "object"
  ) {
    const body = error.error as {
      code?: unknown;
      message?: unknown;
      details?: unknown;
    };
    const code = typeof body.code === "string" ? body.code : "unknown_error";
    const message =
      typeof body.message === "string"
        ? body.message
        : response.statusText || `Request failed (${response.status})`;
    const details =
      body.details && typeof body.details === "object"
        ? (body.details as Record<string, string>)
        : undefined;
    return new ApiError(
      response.status,
      code,
      message,
      details,
      parseRetryAfterSeconds(response, details),
    );
  }
  return new ApiError(
    response.status,
    "http_error",
    response.statusText || `Request failed (${response.status})`,
    undefined,
    parseRetryAfterSeconds(response),
  );
}

export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (!result.response.ok || result.error !== undefined || result.data === undefined) {
    throw toApiError(result.response, result.error);
  }
  return result.data;
}

export function unwrapVoid(result: { error?: unknown; response: Response }): void {
  if (!result.response.ok || result.error !== undefined) {
    throw toApiError(result.response, result.error);
  }
}

export function getApiErrorMessage(error: unknown, fallback = "Something went wrong"): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}

/**
 * Ensures post-login redirects only target safe same-origin relative paths.
 */
export function sanitizeReturnTo(raw: string | undefined): string {
  if (!raw) {
    return "/";
  }
  const trimmed = raw.trim();
  if (!trimmed.startsWith("/") || trimmed.startsWith("//") || trimmed.startsWith("/\\")) {
    return "/";
  }
  if (
    trimmed === "/login" ||
    trimmed.startsWith("/login?") ||
    trimmed === "/setup" ||
    trimmed.startsWith("/setup?")
  ) {
    return "/";
  }
  return trimmed;
}

export function handleUnauthorizedRedirect(error: unknown): boolean {
  if (error instanceof ApiError && error.status === 401 && typeof window !== "undefined") {
    setCsrfToken("");
    const pathname = window.location.pathname;
    if (
      !pathname.startsWith("/login") &&
      !pathname.startsWith("/setup") &&
      !pathname.startsWith("/invite/") &&
      !pathname.startsWith("/reset/")
    ) {
      const returnTo = `${pathname}${window.location.search}`;
      window.location.assign(`/login?returnTo=${encodeURIComponent(returnTo)}`);
    }
    return true;
  }
  return false;
}

export function handleApiError(error: unknown, fallback = "Request failed"): void {
  if (handleUnauthorizedRedirect(error)) {
    return;
  }
  toast.error(getApiErrorMessage(error, fallback));
}
