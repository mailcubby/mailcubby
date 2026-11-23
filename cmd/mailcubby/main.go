// Command mailcubby runs the Mailcubby email testing server and CLI subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Build metadata populated via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	defaultHTTPAddr = ":8025"
	shutdownTimeout = 30 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	subcmd := "serve"
	var subargs []string
	if len(args) > 0 {
		subcmd = args[0]
		subargs = args[1:]
	}

	switch subcmd {
	case "serve":
		return runServe(subargs, stdout, stderr, lookupEnv)
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
	_, _ = fmt.Fprintln(w, "  serve     Run the Mailcubby server (default)")
	_, _ = fmt.Fprintln(w, "  version   Print version, commit, and build date")
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

	addr := defaultHTTPAddr
	if v, ok := lookupEnv("MAILCUBBY_HTTP_ADDR"); ok && v != "" {
		addr = v
	}

	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serveHTTP(ctx, addr, newPlaceholderHandler(), logger); err != nil {
		logger.Error("server exited with error", "error", err)
		return 1
	}
	return 0
}

func newPlaceholderHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "Mailcubby\n")
	})
}

func serveHTTP(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "http_addr", ln.Addr().String(), "version", version)
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return <-errCh
	}
}
