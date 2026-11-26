import { cn } from "@/lib/utils";

/** AUTHOR_URL and COMPANY_URL are the author credit links shown on public pages and in About. */
export const AUTHOR_URL = "https://x.com/fabiomlferreira";
export const COMPANY_URL = "https://noop.pt";
export const SOURCE_URL = "https://github.com/mailcubby/mailcubby";

const LINK_CLASS = "underline underline-offset-2 hover:text-foreground";

/** MadeBy renders the "Made by Fábio Ferreira at NOOP" credit with plain, followable links. */
export function MadeBy({ className }: { className?: string }) {
  return (
    <span className={className}>
      Made by{" "}
      <a href={AUTHOR_URL} target="_blank" rel="noopener" className={LINK_CLASS}>
        Fábio Ferreira
      </a>{" "}
      at{" "}
      <a href={COMPANY_URL} target="_blank" rel="noopener" className={LINK_CLASS}>
        NOOP
      </a>
    </span>
  );
}

/**
 * PublicFooter is the quiet credit line under the card on the sign-in, invite,
 * reset and setup pages. `poweredBy` adds "Powered by Mailcubby" when the
 * instance has its own name (audit V6).
 */
export function PublicFooter({
  poweredBy = false,
  className,
}: {
  poweredBy?: boolean;
  className?: string;
}) {
  return (
    <footer
      className={cn(
        "flex flex-wrap items-center justify-center gap-x-1.5 gap-y-1 text-center text-xs text-muted-foreground",
        className,
      )}
    >
      {poweredBy ? (
        <>
          <span>
            Powered by{" "}
            <a href={SOURCE_URL} target="_blank" rel="noopener" className={LINK_CLASS}>
              Mailcubby
            </a>
          </span>
          <span aria-hidden="true">·</span>
        </>
      ) : null}
      <MadeBy />
    </footer>
  );
}
