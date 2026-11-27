// Command mailcubby runs the Mailcubby email testing server and CLI subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mailcubby/mailcubby/internal/app"
	"github.com/mailcubby/mailcubby/internal/cli"
	"github.com/mailcubby/mailcubby/internal/config"
)

// Build metadata populated via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	subcmd := "serve"
	var subargs []string
	if len(args) > 0 {
		subcmd = args[0]
		subargs = args[1:]
	}

	switch subcmd {
	case "serve":
		return runServe(subargs, stdout, stderr, lookupEnv)
	case "keygen":
		return cli.RunKeygen(subargs, stdout, stderr)
	case "healthcheck":
		return cli.RunHealthcheck(context.Background(), subargs, stdout, stderr, lookupEnv)
	case "migrate":
		return cli.RunMigrate(context.Background(), subargs, stdout, stderr, lookupEnv)
	case "backup":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cli.RunBackup(ctx, subargs, stdout, stderr, lookupEnv)
	case "admin":
		return cli.RunAdmin(context.Background(), subargs, stdout, stderr, lookupEnv)
	case "sendmail":
		return cli.RunSendmail(subargs, stdin, stdout, stderr, lookupEnv)
	case "version", "-version", "--version":
		return runVersion(subargs, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n\n", subcmd)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: mailcubby <command> [args]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  serve        Run the Mailcubby server (default)")
	_, _ = fmt.Fprintln(w, "  keygen       Generate a base64 MAILCUBBY_APP_KEY")
	_, _ = fmt.Fprintln(w, "  healthcheck  Check HTTP /readyz endpoint (exit 0 if healthy)")
	_, _ = fmt.Fprintln(w, "  migrate      Inspect database migration status")
	_, _ = fmt.Fprintln(w, "  backup       Create an online backup of app.db")
	_, _ = fmt.Fprintln(w, "  admin        Administrative commands (create-user, list-users, enable-user, reset-password)")
	_, _ = fmt.Fprintln(w, "  sendmail     Submit a message from stdin over SMTP")
	_, _ = fmt.Fprintln(w, "  version      Print version, commit, and build date")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Run 'mailcubby serve --help' for the serve flags and environment variables.")
}

// printServeUsage documents `mailcubby serve`: it has no flags of its own and
// is configured through MAILCUBBY_* environment variables (SPEC 9).
func printServeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: mailcubby serve [-h | --help]

Run the SMTP server, web UI and REST API. Configuration comes only from
environment variables; with none set, Mailcubby starts in local mode.

Flags:
  -h, --help   Show this help and exit

Common environment variables (defaults in brackets):
  MAILCUBBY_MODE        local (no login) or server (multi-tenant) [local]
  MAILCUBBY_DATA_DIR    database, message files and app.key [./data]
  MAILCUBBY_HTTP_ADDR   web UI and REST API listen address [:8025]
  MAILCUBBY_SMTP_ADDR   SMTP listen address [:1025]
  MAILCUBBY_BASE_URL    public URL, required in server mode
  MAILCUBBY_APP_KEY     base64 32-byte key, required in server mode
                        (or MAILCUBBY_APP_KEY_FILE; create one with: mailcubby keygen)
  MAILCUBBY_LOG_LEVEL   debug, info, warn or error [info]
  MAILCUBBY_LOG_FORMAT  text or json [text]

All variables are listed in the README (Configuration) and docs/SPEC.md section 9.
`)
}

func formatVersion(ver, rev, buildDate string) string {
	return fmt.Sprintf("mailcubby %s (commit %s, built %s)", ver, rev, buildDate)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "version takes no arguments\n")
		return 2
	}
	_, _ = fmt.Fprintln(stdout, formatVersion(version, commit, date))
	return 0
}

func runServe(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printServeUsage(stdout)
			return 0
		}
		printServeUsage(stderr)
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "serve takes no positional arguments\n\n")
		printServeUsage(stderr)
		return 2
	}

	cfg, err := config.Load(lookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid configuration:\n%v\n", err)
		return 1
	}

	logger := newLogger(stdout, cfg.LogLevel, cfg.LogFormat)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, app.Options{
		Logger:  logger,
		Version: version,
	}); err != nil {
		logger.Error("server exited with error", "error", err)
		return 1
	}
	return 0
}

func newLogger(w io.Writer, level slog.Level, format config.LogFormat) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
