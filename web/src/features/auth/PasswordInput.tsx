import { Eye, EyeOff } from "lucide-react";
import { type InputHTMLAttributes, useState } from "react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

export interface PasswordInputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type"> {
  ref?: React.Ref<HTMLInputElement>;
}

/**
 * Accessible password input with a Show/Hide toggle button (`aria-pressed`),
 * preserving browser autofill (`autocomplete`) and paste support.
 */
export function PasswordInput({ className, ref, ...props }: PasswordInputProps) {
  const [visible, setVisible] = useState(false);

  return (
    <div className="relative flex items-center">
      <Input
        ref={ref}
        type={visible ? "text" : "password"}
        className={cn("pr-9", className)}
        {...props}
      />
      <button
        type="button"
        aria-pressed={visible}
        aria-label={visible ? "Hide password" : "Show password"}
        onClick={() => setVisible((prev) => !prev)}
        className="absolute right-1.5 inline-flex size-6 cursor-pointer items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        {visible ? (
          <EyeOff className="size-3.5" aria-hidden="true" />
        ) : (
          <Eye className="size-3.5" aria-hidden="true" />
        )}
      </button>
    </div>
  );
}
