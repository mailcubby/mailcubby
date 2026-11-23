import { toast } from "sonner";

export async function copyToClipboard(text: string, label?: string): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else if (typeof document !== "undefined") {
      const textarea = document.createElement("textarea");
      textarea.value = text;
      textarea.setAttribute("readonly", "");
      textarea.className = "sr-only";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
    }
    toast.success(label ? `Copied ${label}` : "Copied");
    return true;
  } catch {
    toast.error("Failed to copy to clipboard");
    return false;
  }
}
