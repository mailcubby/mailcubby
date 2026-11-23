package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mailcubby/mailcubby/internal/store"
)

const defaultDataDir = "./data"

// RunMigrate implements the "mailcubby migrate <subcommand>" CLI entrypoint.
func RunMigrate(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	if len(args) == 0 {
		printMigrateUsage(stderr)
		return 2
	}

	switch args[0] {
	case "status":
		return runMigrateStatus(ctx, args[1:], stdout, stderr, lookupEnv)
	case "help", "-h", "--help":
		printMigrateUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown migrate subcommand %q\n\n", args[0])
		printMigrateUsage(stderr)
		return 2
	}
}

func printMigrateUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: mailcubby migrate status [--data-dir path]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Subcommands:")
	_, _ = fmt.Fprintln(w, "  status   Show applied and pending database migrations")
}

func runMigrateStatus(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	dataDirDefault := defaultDataDir
	if lookupEnv != nil {
		if v, ok := lookupEnv("MAILCUBBY_DATA_DIR"); ok && strings.TrimSpace(v) != "" {
			dataDirDefault = strings.TrimSpace(v)
		}
	}

	fs := flag.NewFlagSet("migrate status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", dataDirDefault, "data directory containing app.db")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "migrate status takes no positional arguments")
		return 2
	}

	dbPath := filepath.Join(*dataDir, store.DBFilename)
	statuses, err := store.CheckMigrationStatus(ctx, dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate status: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VERSION\tMIGRATION\tSTATE\tAPPLIED AT")
	for _, st := range statuses {
		applied := "-"
		if !st.AppliedAt.IsZero() {
			applied = st.AppliedAt.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", st.Version, st.Source, st.State, applied)
	}
	if err := tw.Flush(); err != nil {
		_, _ = fmt.Fprintf(stderr, "migrate status: flush output: %v\n", err)
		return 1
	}
	return 0
}
