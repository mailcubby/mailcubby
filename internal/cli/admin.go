package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/store"
)

// RunAdmin implements the "mailcubby admin <subcommand>" CLI entrypoint (SPEC FR-115).
func RunAdmin(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	return RunAdminWithClock(ctx, args, stdout, stderr, lookupEnv, clock.NewReal())
}

// RunAdminWithClock runs the admin subcommand using an injected clock (for tests).
func RunAdminWithClock(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), clk clock.Clock) int {
	if len(args) == 0 {
		printAdminUsage(stderr)
		return 2
	}

	switch args[0] {
	case "reset-password":
		return runAdminResetPassword(ctx, args[1:], stdout, stderr, lookupEnv, clk)
	case "help", "-h", "--help":
		printAdminUsage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown admin subcommand %q\n\n", args[0])
		printAdminUsage(stderr)
		return 2
	}
}

func printAdminUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: mailcubby admin <subcommand> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Subcommands:")
	_, _ = fmt.Fprintln(w, "  reset-password --email <email>  Generate a single-use password reset link (valid 1h)")
}

func runAdminResetPassword(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), clk clock.Clock) int {
	dataDirDefault := defaultDataDir
	baseURLDefault := ""
	if lookupEnv != nil {
		if v, ok := lookupEnv("MAILCUBBY_DATA_DIR"); ok && strings.TrimSpace(v) != "" {
			dataDirDefault = strings.TrimSpace(v)
		}
		if v, ok := lookupEnv("MAILCUBBY_BASE_URL"); ok && strings.TrimSpace(v) != "" {
			baseURLDefault = strings.TrimSpace(v)
		}
	}

	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	emailFlag := fs.String("email", "", "email address of the user to reset")
	dataDirFlag := fs.String("data-dir", dataDirDefault, "data directory containing app.db")
	baseURLFlag := fs.String("base-url", baseURLDefault, "public base URL (default: MAILCUBBY_BASE_URL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "admin reset-password takes no positional arguments")
		return 2
	}

	email := strings.TrimSpace(*emailFlag)
	if email == "" {
		_, _ = fmt.Fprintln(stderr, "admin reset-password: --email is required")
		return 2
	}
	baseURL := strings.TrimRight(strings.TrimSpace(*baseURLFlag), "/")
	if baseURL == "" {
		_, _ = fmt.Fprintln(stderr, "admin reset-password: MAILCUBBY_BASE_URL or --base-url is required")
		return 1
	}
	if parsed, err := url.Parse(baseURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		_, _ = fmt.Fprintf(stderr, "admin reset-password: invalid base URL %q\n", baseURL)
		return 1
	}

	if clk == nil {
		clk = clock.NewReal()
	}

	st, err := store.Open(ctx, filepath.Join(*dataDirFlag, store.DBFilename))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "admin reset-password: open store: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	user, err := st.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = fmt.Fprintf(stderr, "admin reset-password: user %q not found\n", email)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "admin reset-password: lookup user: %v\n", err)
		return 1
	}

	token, err := auth.GenerateToken()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "admin reset-password: generate token: %v\n", err)
		return 1
	}
	now := clk.Now().UTC()
	expiresAt := now.Add(auth.PasswordResetTTL)

	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.CreatePasswordReset(ctx, store.CreatePasswordResetParams{
			UserID:    user.ID,
			TokenHash: auth.HashToken(token),
			Now:       now,
			ExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		actorLabel := "cli"
		targetType := "user"
		targetID := strconv.FormatInt(user.ID, 10)
		_, err := tx.InsertAudit(ctx, store.InsertAuditParams{
			At:         now,
			ActorType:  store.AuditActorSystem,
			ActorLabel: &actorLabel,
			Action:     "user.password_reset_link",
			TargetType: &targetType,
			TargetID:   &targetID,
			Metadata:   map[string]any{"expires_at": expiresAt.Format(time.RFC3339)},
		})
		return err
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "admin reset-password: create reset token: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "%s/reset/%s\n", baseURL, token)
	return 0
}
