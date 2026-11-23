import createClient from "openapi-fetch";
import { toast } from "sonner";
import type { components, paths } from "@/api/schema";

export const api = createClient<paths>({
  baseUrl: "",
});

export type ProjectColor = components["schemas"]["ProjectColor"];
export type ProjectRole = components["schemas"]["ProjectRole"];
export type MetaResponse = components["schemas"]["MetaResponse"];
export type AuthMeResponse = components["schemas"]["AuthMeResponse"];
export type ProjectSummary = components["schemas"]["ProjectSummary"];
export type ProjectDetail = components["schemas"]["ProjectDetail"];
export type ProjectSettings = components["schemas"]["ProjectSettings"];
export type CreateProjectRequest = components["schemas"]["CreateProjectRequest"];
export type Address = components["schemas"]["Address"];
export type Header = components["schemas"]["Header"];
export type AttachmentSummary = components["schemas"]["AttachmentSummary"];
export type MessageEnvelope = components["schemas"]["MessageEnvelope"];
export type MessageSummary = components["schemas"]["MessageSummary"];
export type MessageListResponse = components["schemas"]["MessageListResponse"];
export type MessageDetail = components["schemas"]["MessageDetail"];
export type BulkMessagesRequest = components["schemas"]["BulkMessagesRequest"];
export type ErrorEnvelope = components["schemas"]["ErrorEnvelope"];

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: Record<string, string> | undefined;

  constructor(status: number, code: string, message: string, details?: Record<string, string>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
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
    return new ApiError(response.status, code, message, details);
  }
  return new ApiError(
    response.status,
    "http_error",
    response.statusText || `Request failed (${response.status})`,
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

export function handleUnauthorizedRedirect(error: unknown): boolean {
  if (error instanceof ApiError && error.status === 401 && typeof window !== "undefined") {
    const returnTo = `${window.location.pathname}${window.location.search}`;
    if (!window.location.pathname.startsWith("/login")) {
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
