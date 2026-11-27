import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PdfPreviewDialog } from "@/features/message/PdfPreviewDialog";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes } from "@/lib/format";

function makePdf(overrides?: Partial<AttachmentSummary>): AttachmentSummary {
  return {
    id: 12,
    filename: "invoice.pdf",
    content_type: "application/pdf",
    size_bytes: 4096,
    content_id: null,
    disposition: "attachment",
    referenced_by_html: false,
    ...overrides,
  };
}

describe("PdfPreviewDialog (FR-074, ADR 0009)", () => {
  beforeEach(() => {
    // jsdom does not implement HTMLDialogElement.showModal / close natively.
    HTMLDialogElement.prototype.showModal = vi.fn(function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    });
    HTMLDialogElement.prototype.close = vi.fn(function (this: HTMLDialogElement) {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("renders an unsandboxed no-referrer iframe pointing to /render/.../attachments/... with header and footer actions", () => {
    const onOpenChange = vi.fn();
    render(
      <PdfPreviewDialog
        open
        onOpenChange={onOpenChange}
        projectSlug="shop"
        messageId="01MSG"
        attachment={makePdf()}
      />,
    );

    expect(screen.getByRole("heading", { name: "invoice.pdf" })).toBeTruthy();
    expect(screen.getByText(`${formatBytes(4096)} · PDF preview`)).toBeTruthy();

    const iframe = screen.getByTitle("PDF preview: invoice.pdf");
    expect(iframe.tagName).toBe("IFRAME");
    expect(iframe.getAttribute("src")).toBe("/render/shop/01MSG/attachments/12");
    expect(iframe.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(iframe.hasAttribute("sandbox")).toBe(false);

    // Header and footer both provide Open in new tab and Download actions (ADR 0009 mobile fallback).
    const openLinks = screen.getAllByRole("link", { name: "Open in new tab" });
    expect(openLinks).toHaveLength(2);
    for (const link of openLinks) {
      expect(link.getAttribute("href")).toBe("/render/shop/01MSG/attachments/12");
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noopener noreferrer");
    }

    const downloadLinks = screen.getAllByRole("link", { name: "Download" });
    expect(downloadLinks).toHaveLength(2);
    for (const link of downloadLinks) {
      expect(link.getAttribute("href")).toBe("/api/v1/projects/shop/messages/01MSG/attachments/12");
      expect(link.getAttribute("download")).toBe("invoice.pdf");
    }

    expect(screen.getByText(/If your browser cannot display PDFs inline/i)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Close dialog" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renders no iframe when closed or attachment is null", () => {
    render(
      <PdfPreviewDialog
        open={false}
        onOpenChange={vi.fn()}
        projectSlug="shop"
        messageId="01MSG"
        attachment={null}
      />,
    );

    expect(screen.queryByTitle(/PDF preview:/)).toBeNull();
  });
});
