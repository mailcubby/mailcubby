import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export interface SwitchProps extends Omit<ComponentProps<"input">, "type" | "role" | "checked"> {
  checked: boolean;
}

/**
 * Switch is a native checkbox with `role="switch"`, drawn as a track and knob.
 * The input itself is the track (so it keeps native keyboard, form, label and
 * focus-ring behaviour); the knob is a sibling element, not a pseudo-element
 * of the input, because `::before` on form controls is not reliable outside
 * Chromium. `className` applies to the wrapper; every other prop goes to the
 * input.
 */
export function Switch({ checked, className, ...props }: SwitchProps) {
  return (
    <span className={cn("relative inline-flex h-[18px] w-8 shrink-0", className)}>
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        aria-checked={checked}
        className="peer m-0 size-full cursor-pointer appearance-none rounded-full bg-input transition-colors duration-150 checked:bg-primary disabled:cursor-not-allowed disabled:opacity-50 motion-reduce:transition-none"
        {...props}
      />
      <span
        aria-hidden="true"
        className="pointer-events-none absolute top-0.5 left-0.5 size-3.5 rounded-full bg-surface shadow-xs transition-transform duration-150 peer-checked:translate-x-3.5 peer-disabled:opacity-50 motion-reduce:transition-none"
      />
    </span>
  );
}
