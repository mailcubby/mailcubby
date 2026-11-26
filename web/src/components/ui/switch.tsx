import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export interface SwitchProps extends Omit<ComponentProps<"input">, "type" | "role" | "checked"> {
  checked: boolean;
}

/**
 * Switch is a native checkbox with `role="switch"`, drawn as a track and knob.
 * It keeps native keyboard (Space), form and label behaviour.
 */
export function Switch({ checked, className, ...props }: SwitchProps) {
  return (
    <input
      type="checkbox"
      role="switch"
      checked={checked}
      aria-checked={checked}
      className={cn(
        "relative h-[18px] w-8 shrink-0 cursor-pointer appearance-none rounded-full bg-input transition-colors duration-150 checked:bg-primary disabled:cursor-not-allowed disabled:opacity-50",
        "before:pointer-events-none before:absolute before:top-0.5 before:left-0.5 before:size-3.5 before:rounded-full before:bg-surface before:shadow-xs before:transition-transform before:duration-150 checked:before:translate-x-3.5 motion-reduce:before:transition-none",
        className,
      )}
      {...props}
    />
  );
}
