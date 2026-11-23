// Package cli implements Mailcubby CLI subcommands (SPEC FR-115).
package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/mailcubby/mailcubby/internal/secrets"
)

// RunKeygen implements the "mailcubby keygen" subcommand, printing a fresh
// base64-encoded 32-byte MAILCUBBY_APP_KEY to stdout.
func RunKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "keygen takes no arguments")
		return 2
	}

	_, encoded, err := secrets.GenerateAppKey()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "keygen: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, encoded)
	return 0
}
