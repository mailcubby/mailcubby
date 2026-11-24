// Command mailcubby runs the Mailcubby email testing server and CLI subcommands.
package main

import (
	"context"
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
	_, _ = fmt.Fprintln(w, "  admin        Administrative commands (reset-password)")
	_, _ = fmt.Fprintln(w, "  sendmail     Submit a message from stdin over SMTP")
	_, _ = fmt.Fprintln(w, "  version      Print version, commit, and build date")
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "serve takes no positional arguments\n")
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
