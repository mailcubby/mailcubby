import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AttachmentsTab } from "@/features/message/AttachmentsTab";
import type { AttachmentSummary } from "@/lib/api";

function makeAtt(
  overrides: Partial<AttachmentSummary> & Pick<AttachmentSummary, "id">,
): AttachmentSummary {
  return {
    filename: `file-${overrides.id}.bin`,
    content_type: "application/octet-stream",
    size_bytes: 1024,
    content_id: null,
    disposition: "attachment",
    referenced_by_html: false,
    ...overrides,
  };
}

describe("AttachmentsTab (FR-072, ADR 0009)", () => {
  afterEach(() => {
    cleanup();
  });

  it("shows Preview button for PDF rows only when inlinePdfEnabled is true", () => {
    const onPreviewPdf = vi.fn();
    const pdf = makeAtt({
      id: 1,
      filename: "receipt.pdf",
      content_type: "application/pdf",
      size_bytes: 2048,
    });
    const png = makeAtt({
      id: 2,
      filename: "logo.png",
      content_type: "image/png",
      size_bytes: 512,
    });

    const { rerender } = render(
      <AttachmentsTab
        projectSlug="shop"
        messageId="01MSG"
        attachments={[pdf, png]}
        inlinePdfEnabled
        onPreviewPdf={onPreviewPdf}
      />,
    );

    const previewBtn = screen.getByRole("button", { name: "Preview receipt.pdf" });
    fireEvent.click(previewBtn);
    expect(onPreviewPdf).toHaveBeenCalledTimes(1);
    expect(onPreviewPdf).toHaveBeenCalledWith(pdf);

    // Non-PDF rows have no Preview button.
    expect(screen.queryByRole("button", { name: /logo\.png/ })).toBeNull();
    expect(screen.getByRole("link", { name: "Download receipt.pdf" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Download logo.png" })).toBeTruthy();

    // When inlinePdfEnabled is false, the Preview button disappears.
    rerender(
      <AttachmentsTab
        projectSlug="shop"
        messageId="01MSG"
        attachments={[pdf, png]}
        inlinePdfEnabled={false}
        onPreviewPdf={onPreviewPdf}
      />,
    );
    expect(screen.queryByRole("button", { name: /Preview/ })).toBeNull();
  });
});
