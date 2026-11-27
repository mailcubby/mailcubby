import {
  Calendar,
  File,
  FileArchive,
  FileAudio,
  FileImage,
  FileText,
  FileVideo,
  type LucideIcon,
} from "lucide-react";

/** AttachmentKind is the coarse type used to pick an attachment icon. */
export type AttachmentKind =
  | "image"
  | "pdf"
  | "calendar"
  | "archive"
  | "audio"
  | "video"
  | "text"
  | "file";

const ARCHIVE_TYPES: ReadonlySet<string> = new Set([
  "application/zip",
  "application/x-zip-compressed",
  "application/gzip",
  "application/x-gzip",
  "application/x-tar",
  "application/x-7z-compressed",
  "application/x-rar-compressed",
  "application/vnd.rar",
  "application/x-bzip2",
  "application/x-xz",
]);

const ICONS: Record<AttachmentKind, LucideIcon> = {
  image: FileImage,
  pdf: FileText,
  calendar: Calendar,
  archive: FileArchive,
  audio: FileAudio,
  video: FileVideo,
  text: FileText,
  file: File,
};

/** getAttachmentKind classifies a MIME content type for icon selection. */
export function getAttachmentKind(contentType: string): AttachmentKind {
  const mime = contentType.split(";")[0]?.trim().toLowerCase() ?? "";
  if (mime === "text/calendar" || mime === "application/ics") {
    return "calendar";
  }
  if (mime === "application/pdf") {
    return "pdf";
  }
  if (ARCHIVE_TYPES.has(mime)) {
    return "archive";
  }
  if (mime.startsWith("image/")) {
    return "image";
  }
  if (mime.startsWith("audio/")) {
    return "audio";
  }
  if (mime.startsWith("video/")) {
    return "video";
  }
  if (mime.startsWith("text/")) {
    return "text";
  }
  return "file";
}

/** getAttachmentIcon returns the lucide icon for a MIME content type. */
export function getAttachmentIcon(contentType: string): LucideIcon {
  return ICONS[getAttachmentKind(contentType)];
}

/** isPdfMime reports whether a MIME content type is application/pdf. */
export function isPdfMime(contentType: string): boolean {
  return getAttachmentKind(contentType) === "pdf";
}

/** attachmentUrl returns the download URL of an attachment (FR-074). */
export function attachmentUrl(
  projectSlug: string,
  messageId: string,
  attachmentId: number,
): string {
  return `/api/v1/projects/${encodeURIComponent(projectSlug)}/messages/${encodeURIComponent(messageId)}/attachments/${attachmentId}`;
}

/**
 * attachmentPdfRenderUrl returns the dedicated inline PDF render URL served
 * under the ADR 0009 PDF-specific CSP (FR-074, SPEC 12.2).
 */
export function attachmentPdfRenderUrl(
  projectSlug: string,
  messageId: string,
  attachmentId: number,
): string {
  return `/render/${encodeURIComponent(projectSlug)}/${encodeURIComponent(messageId)}/attachments/${attachmentId}`;
}
