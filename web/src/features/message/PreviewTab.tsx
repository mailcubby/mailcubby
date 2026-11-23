import { useQuery } from "@tanstack/react-query";
import { Image, ImageOff, Monitor, Moon, RotateCw, Smartphone, Sun, Tablet } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import type { MessageDetail } from "@/lib/api";
import { metaQueryOptions } from "@/lib/queries";
import { usePersistentState } from "@/lib/usePersistentState";
import { cn } from "@/lib/utils";

type DeviceKind = "desktop" | "tablet" | "mobile";
type ZoomOption = "fit" | 50 | 75 | 100 | 125 | 150 | 200;

function isDeviceKind(value: unknown): value is DeviceKind {
  return value === "desktop" || value === "tablet" || value === "mobile";
}

const DEVICE_DIMENSIONS: Record<
  Exclude<DeviceKind, "desktop">,
  { width: number; height: number }
> = {
  tablet: { width: 768, height: 1024 },
  mobile: { width: 375, height: 667 },
};

const ZOOM_OPTIONS: ReadonlyArray<{ value: ZoomOption; label: string }> = [
  { value: "fit", label: "Fit" },
  { value: 50, label: "50%" },
  { value: 75, label: "75%" },
  { value: 100, label: "100%" },
  { value: 125, label: "125%" },
  { value: 150, label: "150%" },
  { value: 200, label: "200%" },
];

export interface PreviewTabProps {
  message: MessageDetail;
}

export function PreviewTab({ message }: PreviewTabProps) {
  const { data: meta } = useQuery(metaQueryOptions());
  const [device, setDevice] = usePersistentState<DeviceKind>(
    "preview-device",
    "desktop",
    isDeviceKind,
  );
  const [rotated, setRotated] = useState(false);
  const [zoom, setZoom] = useState<ZoomOption>("fit");
  const [emailDarkMode, setEmailDarkMode] = useState(false);
  // Remote images opt-in is strictly per-message and never persisted (UI 4 item 9).
  const [loadRemoteImages, setLoadRemoteImages] = useState(false);

  if (!message.render_url) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
        This message does not have an HTML part.
      </div>
    );
  }

  const remoteImagesPolicy = meta?.features.remote_images ?? "opt-in";
  const allowRemoteToggle = remoteImagesPolicy === "opt-in";
  const iframeSrc =
    allowRemoteToggle && loadRemoteImages ? `${message.render_url}?images=1` : message.render_url;

  const subject = message.subject.trim() || "(no subject)";
  const baseDims = device === "desktop" ? null : DEVICE_DIMENSIONS[device];
  const frameWidth = baseDims ? (rotated ? baseDims.height : baseDims.width) : null;
  const frameHeight = baseDims ? (rotated ? baseDims.width : baseDims.height) : null;
  const scale = zoom === "fit" ? 1 : zoom / 100;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-surface px-3 py-1.5 text-xs">
        <div className="flex flex-wrap items-center gap-1.5">
          <fieldset
            aria-label="Preview device size"
            className="inline-flex items-center gap-0.5 rounded-md border border-border bg-background p-0.5"
          >
            <legend className="sr-only">Preview device size</legend>
            {(
              [
                { value: "desktop", label: "Desktop", Icon: Monitor },
                { value: "tablet", label: "Tablet", Icon: Tablet },
                { value: "mobile", label: "Mobile", Icon: Smartphone },
              ] as const
            ).map(({ value, label, Icon }) => (
              <button
                key={value}
                type="button"
                aria-pressed={device === value}
                aria-label={label}
                title={label}
                onClick={() => {
                  setDevice(value);
                  if (value === "desktop") {
                    setRotated(false);
                  }
                }}
                className={cn(
                  "inline-flex h-6 min-w-6 cursor-pointer items-center justify-center gap-1 rounded-sm px-2 text-xs transition-colors",
                  device === value
                    ? "bg-surface font-medium text-foreground shadow-2xs"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="size-3.5" aria-hidden="true" />
                <span className="hidden sm:inline">{label}</span>
              </button>
            ))}
          </fieldset>

          {device !== "desktop" ? (
            <Button
              variant="outline"
              size="xs"
              aria-label="Rotate device"
              aria-pressed={rotated}
              title="Rotate viewport"
              onClick={() => setRotated((prev) => !prev)}
            >
              <RotateCw className="size-3" aria-hidden="true" />
              <span className="tabular-nums">
                {frameWidth}×{frameHeight}
              </span>
            </Button>
          ) : null}

          <label className="inline-flex items-center gap-1 text-muted-foreground">
            <span className="sr-only">Zoom level</span>
            <select
              aria-label="Preview zoom"
              value={String(zoom)}
              onChange={(e) => {
                const raw = e.target.value;
                setZoom(raw === "fit" ? "fit" : (Number(raw) as ZoomOption));
              }}
              className="h-7 cursor-pointer rounded-md border border-border bg-surface px-2 text-xs text-foreground"
            >
              {ZOOM_OPTIONS.map((opt) => (
                <option key={String(opt.value)} value={String(opt.value)}>
                  {opt.label}
                </option>
              ))}
            </select>
          </label>

          <Button
            variant={emailDarkMode ? "secondary" : "outline"}
            size="xs"
            aria-pressed={emailDarkMode}
            onClick={() => setEmailDarkMode((prev) => !prev)}
            title="Toggle email dark color-scheme"
          >
            {emailDarkMode ? (
              <Moon className="size-3" aria-hidden="true" />
            ) : (
              <Sun className="size-3" aria-hidden="true" />
            )}
            <span>Email dark</span>
          </Button>
        </div>

        {message.remote_resource_count > 0 ? (
          <div className="flex items-center gap-2">
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground tabular-nums">
              {loadRemoteImages ? (
                <Image className="size-3.5 text-accent" aria-hidden="true" />
              ) : (
                <ImageOff className="size-3.5" aria-hidden="true" />
              )}
              {loadRemoteImages
                ? `Showing ${message.remote_resource_count} remote ${message.remote_resource_count === 1 ? "image" : "images"}`
                : `${message.remote_resource_count} remote ${message.remote_resource_count === 1 ? "image" : "images"} blocked`}
            </span>
            {allowRemoteToggle ? (
              <Button
                variant="outline"
                size="xs"
                onClick={() => setLoadRemoteImages((prev) => !prev)}
              >
                {loadRemoteImages ? "Block remote images" : "Load remote images"}
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-3">
        {device === "desktop" ? (
          <div
            className="h-full w-full overflow-hidden rounded-md border border-border bg-white"
            style={
              scale !== 1
                ? {
                    width: `${100 / scale}%`,
                    height: `${100 / scale}%`,
                    transform: `scale(${scale})`,
                    transformOrigin: "top left",
                  }
                : undefined
            }
          >
            <iframe
              title={`Email preview: ${subject}`}
              src={iframeSrc}
              sandbox="allow-popups allow-popups-to-escape-sandbox"
              referrerPolicy="no-referrer"
              style={{ colorScheme: emailDarkMode ? "dark" : "light" }}
              className={cn("h-full w-full border-0", emailDarkMode ? "bg-zinc-950" : "bg-white")}
            />
          </div>
        ) : (
          <div className="flex min-h-full flex-col items-center justify-start py-2">
            <div className="mb-1.5 text-[11px] font-mono text-muted-foreground tabular-nums">
              {frameWidth} × {frameHeight}px
              {zoom !== "fit" ? ` (${zoom}%)` : ""}
            </div>
            <div
              className="overflow-hidden rounded-lg border border-border bg-white shadow-sm"
              style={{
                width: `${frameWidth ?? 375}px`,
                height: `${frameHeight ?? 667}px`,
                transform: scale !== 1 ? `scale(${scale})` : undefined,
                transformOrigin: "top center",
              }}
            >
              <iframe
                title={`Email preview: ${subject}`}
                src={iframeSrc}
                sandbox="allow-popups allow-popups-to-escape-sandbox"
                referrerPolicy="no-referrer"
                style={{ colorScheme: emailDarkMode ? "dark" : "light" }}
                className={cn("h-full w-full border-0", emailDarkMode ? "bg-zinc-950" : "bg-white")}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
