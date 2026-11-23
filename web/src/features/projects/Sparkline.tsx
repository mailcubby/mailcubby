import { cn } from "@/lib/utils";

export interface SparklineProps {
  values: ReadonlyArray<number>;
  className?: string;
}

/**
 * 24-hour message volume sparkline (UI 3.3):
 * 24 bars oldest-first, muted colour, accent for the latest hour if non-zero.
 */
export function Sparkline({ values, className }: SparklineProps) {
  const padded =
    values.length >= 24 ? values.slice(-24) : [...Array(24 - values.length).fill(0), ...values];
  const max = Math.max(1, ...padded);
  const total = padded.reduce((acc, v) => acc + v, 0);

  const barWidth = 3;
  const gap = 1.5;
  const height = 24;
  const width = padded.length * barWidth + (padded.length - 1) * gap;

  const bars = padded.map((count, idx) => ({
    id: `h-${padded.length - 1 - idx}`,
    count,
    isLatest: idx === padded.length - 1,
    x: idx * (barWidth + gap),
  }));

  return (
    <svg
      role="img"
      aria-label={`Last 24 hours: ${total} ${total === 1 ? "message" : "messages"}`}
      viewBox={`0 0 ${width} ${height}`}
      className={cn("h-6 w-28 shrink-0 overflow-visible", className)}
    >
      {bars.map(({ id, count, isLatest, x }) => {
        const barHeight = count > 0 ? Math.max(3, Math.round((count / max) * (height - 2))) : 2;
        const y = height - barHeight;
        const fillClass =
          isLatest && count > 0
            ? "fill-accent"
            : count > 0
              ? "fill-muted-foreground/60"
              : "fill-border";

        return (
          <rect
            key={id}
            x={x}
            y={y}
            width={barWidth}
            height={barHeight}
            rx={1}
            className={fillClass}
          />
        );
      })}
    </svg>
  );
}
