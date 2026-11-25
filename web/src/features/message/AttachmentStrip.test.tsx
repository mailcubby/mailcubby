import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { AttachmentStrip } from "@/features/message/AttachmentStrip";
import { getAttachmentKind } from "@/features/message/attachments";
import type { AttachmentSummary } from "@/lib/api";
import { formatBytes } from "@/lib/format";

function att(
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

function renderStrip(attachments: AttachmentSummary[]) {
  return render(<AttachmentStrip projectSlug="shop" messageId="01MSG" attachments={attachments} />);
}

describe("AttachmentStrip (FR-072)", () => {
  afterEach(() => {
    cleanup();
  });

  it("renders nothing when there are no attachments", () => {
    const { container } = renderStrip([]);
    expect(container.innerHTML).toBe("");
  });

  it("renders nothing when every attachment is referenced by the HTML via cid:", () => {
    const { container } = renderStrip([
      att({
        id: 1,
        filename: "logo.png",
        content_type: "image/png",
        content_id: "logo@x",
        disposition: "inline",
        referenced_by_html: true,
      }),
    ]);
    expect(container.innerHTML).toBe("");
  });

  it("shows one download card per strip-eligible attachment with size and thumbnail rules", () => {
    renderStrip([
      att({ id: 1, filename: "invoice.pdf", content_type: "application/pdf", size_bytes: 2048 }),
      att({ id: 2, filename: "photo.png", content_type: "image/png", size_bytes: 5 * 1024 * 1024 }),
      att({ id: 3, filename: "drawing.svg", content_type: "image/svg+xml", size_bytes: 300 }),
      att({
        id: 4,
        filename: "logo.png",
        content_type: "image/png",
        content_id: "logo@x",
        disposition: "inline",
        referenced_by_html: true,
      }),
    ]);

    const strip = screen.getByRole("region", { name: "Attachments" });
    const links = within(strip).getAllByRole("link");
    expect(links).toHaveLength(3);
    expect(within(strip).getByText("3 attachments")).toBeTruthy();
    expect(within(strip).queryByText("logo.png")).toBeNull();

    const pdf = within(strip).getByRole("link", {
      name: `Download invoice.pdf, ${formatBytes(2048)}`,
    });
    expect(pdf.getAttribute("href")).toBe("/api/v1/projects/shop/messages/01MSG/attachments/1");
    expect(pdf.getAttribute("download")).toBe("invoice.pdf");
    expect(pdf.getAttribute("data-kind")).toBe("pdf");
    expect(pdf.querySelector("img")).toBeNull();
    expect(pdf.querySelector("svg")).not.toBeNull();

    const png = within(strip).getByRole("link", {
      name: `Download photo.png, ${formatBytes(5 * 1024 * 1024)}`,
    });
    expect(png.querySelector("img")?.getAttribute("src")).toBe(
      "/api/v1/projects/shop/messages/01MSG/attachments/2?inline=1",
    );

    // SVG is not an inline-allowlisted raster type: icon only, never a thumbnail.
    const svg = within(strip).getByRole("link", { name: /drawing\.svg/ });
    expect(svg.querySelector("img")).toBeNull();
    expect(svg.getAttribute("data-kind")).toBe("image");
  });

  it("falls back to a generated filename and singular label", () => {
    renderStrip([att({ id: 7, filename: "" })]);
    const link = screen.getByRole("link", { name: /attachment-7/ });
    expect(link.getAttribute("download")).toBe("attachment-7");
    expect(screen.getByText("1 attachment")).toBeTruthy();
  });
});

describe("getAttachmentKind", () => {
  it.each([
    ["image/png", "image"],
    ["application/pdf", "pdf"],
    ["text/calendar; method=REQUEST", "calendar"],
    ["application/zip", "archive"],
    ["application/x-7z-compressed", "archive"],
    ["text/plain; charset=utf-8", "text"],
    ["audio/mpeg", "audio"],
    ["video/mp4", "video"],
    ["application/octet-stream", "file"],
    ["", "file"],
  ])("%s -> %s", (contentType, kind) => {
    expect(getAttachmentKind(contentType)).toBe(kind);
  });
});
