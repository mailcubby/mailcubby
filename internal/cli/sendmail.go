package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/smtp"
	"strings"
)

const defaultSendmailSMTP = "localhost:1025"

type sendmailOptions struct {
	smtpAddr    string
	user        string
	password    string
	from        string
	fromSet     bool
	readHeaders bool
	showHelp    bool
	recipients  []string
}

// RunSendmail implements the "mailcubby sendmail" CLI subcommand (SPEC FR-115).
// It parses sendmail-compatible flags by hand, reads a raw RFC 5322 message from
// stdin, and submits it via SMTP.
func RunSendmail(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	smtpDefault := defaultSendmailSMTP
	if lookupEnv != nil {
		if v, ok := lookupEnv("MAILCUBBY_SENDMAIL_SMTP"); ok && strings.TrimSpace(v) != "" {
			smtpDefault = strings.TrimSpace(v)
		}
	}

	opts, err := parseSendmailArgs(args, smtpDefault)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sendmail: %v\n", err)
		return 2
	}
	if opts.showHelp {
		printSendmailUsage(stdout)
		return 0
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sendmail: read stdin: %v\n", err)
		return 1
	}

	from := opts.from
	rcpts := append([]string(nil), opts.recipients...)

	if !opts.fromSet || opts.readHeaders {
		hdrFrom, hdrRcpts := extractEnvelopeFromHeaders(raw)
		if !opts.fromSet && hdrFrom != "" {
			from = hdrFrom
		}
		if opts.readHeaders {
			rcpts = append(rcpts, hdrRcpts...)
		}
	}

	rcpts = dedupeAddresses(rcpts)
	if len(rcpts) == 0 {
		_, _ = fmt.Fprintln(stderr, "sendmail: no recipients specified (pass recipient addresses or -t)")
		return 2
	}

	var auth smtp.Auth
	if opts.user != "" {
		auth = &sendmailPlainAuth{user: opts.user, pass: opts.password}
	}

	if err := submitSMTP(opts.smtpAddr, auth, from, rcpts, raw); err != nil {
		_, _ = fmt.Fprintf(stderr, "sendmail: %v\n", err)
		return 1
	}
	return 0
}

func printSendmailUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: mailcubby sendmail [-t] [-f from] [--smtp host:port] [--user u --password p] [to...]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Reads a raw RFC 5322 email from stdin and submits it over SMTP.")
	_, _ = fmt.Fprintln(w, "Common sendmail flags (-i, -oi, -odi, -bm, -F name) are accepted and ignored.")
}

func parseSendmailArgs(args []string, defaultSMTP string) (sendmailOptions, error) {
	opts := sendmailOptions{smtpAddr: defaultSMTP}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.recipients = append(opts.recipients, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			opts.recipients = append(opts.recipients, arg)
			continue
		}

		// Long options (--flag, -flag, --flag=value).
		if name, val, hasEq := splitLongOpt(arg); name != "" {
			switch name {
			case "help":
				opts.showHelp = true
				return opts, nil
			case "smtp":
				v, nextIdx, err := consumeOptValue(name, val, hasEq, args, i)
				if err != nil {
					return opts, err
				}
				opts.smtpAddr = v
				i = nextIdx
				continue
			case "user":
				v, nextIdx, err := consumeOptValue(name, val, hasEq, args, i)
				if err != nil {
					return opts, err
				}
				opts.user = v
				i = nextIdx
				continue
			case "password":
				v, nextIdx, err := consumeOptValue(name, val, hasEq, args, i)
				if err != nil {
					return opts, err
				}
				opts.password = v
				i = nextIdx
				continue
			default:
				return opts, fmt.Errorf("unknown flag %q", arg)
			}
		}

		// Sendmail-style short and multi-letter flags.
		switch {
		case arg == "-h":
			opts.showHelp = true
			return opts, nil
		case arg == "-t":
			opts.readHeaders = true
		case arg == "-f" || arg == "-r":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("flag %s requires an address", arg)
			}
			i++
			opts.from = strings.TrimSpace(args[i])
			opts.fromSet = true
		case strings.HasPrefix(arg, "-f") || strings.HasPrefix(arg, "-r"):
			opts.from = strings.TrimSpace(arg[2:])
			opts.fromSet = true
		case arg == "-F" || arg == "-B" || arg == "-N" || arg == "-R" || arg == "-V":
			// Ignored sendmail flags that take a following argument.
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(arg, "-F") || strings.HasPrefix(arg, "-B") || strings.HasPrefix(arg, "-N"):
			// Ignored sendmail flags with attached value (e.g. -F"PHP Mailer").
		case arg == "-i" || arg == "-oi" || arg == "-odi" || arg == "-bm" || arg == "-oem" || arg == "-oee" || arg == "-v":
			// Accepted and ignored per SPEC FR-115.
		case strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "-b"):
			// Other -oX / -bX sendmail delivery options are ignored.
		default:
			// Handle bundled single-char flags like -ti or -it.
			if !parseBundledFlags(arg[1:], &opts) {
				return opts, fmt.Errorf("unknown flag %q", arg)
			}
		}
	}

	return opts, nil
}

func splitLongOpt(arg string) (name, val string, hasEq bool) {
	trimmed := ""
	switch {
	case strings.HasPrefix(arg, "--"):
		trimmed = arg[2:]
	case arg == "-smtp" || strings.HasPrefix(arg, "-smtp="):
		trimmed = arg[1:]
	case arg == "-user" || strings.HasPrefix(arg, "-user="):
		trimmed = arg[1:]
	case arg == "-password" || strings.HasPrefix(arg, "-password="):
		trimmed = arg[1:]
	case arg == "-help":
		trimmed = arg[1:]
	default:
		return "", "", false
	}
	if n, v, ok := strings.Cut(trimmed, "="); ok {
		return n, v, true
	}
	return trimmed, "", false
}

func consumeOptValue(name, eqVal string, hasEq bool, args []string, idx int) (string, int, error) {
	if hasEq {
		return eqVal, idx, nil
	}
	if idx+1 >= len(args) {
		return "", idx, fmt.Errorf("flag --%s requires a value", name)
	}
	return args[idx+1], idx + 1, nil
}

func parseBundledFlags(chars string, opts *sendmailOptions) bool {
	if chars == "" {
		return false
	}
	for i := 0; i < len(chars); i++ {
		switch chars[i] {
		case 't':
			opts.readHeaders = true
		case 'i', 'v':
			// ignored
		default:
			return false
		}
	}
	return true
}

func extractEnvelopeFromHeaders(raw []byte) (from string, rcpts []string) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", nil
	}
	if fromList, err := msg.Header.AddressList("From"); err == nil && len(fromList) > 0 {
		from = strings.TrimSpace(fromList[0].Address)
	}
	for _, hdr := range []string{"To", "Cc", "Bcc"} {
		for _, val := range msg.Header[hdr] {
			addrs, err := mail.ParseAddressList(val)
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if addr := strings.TrimSpace(a.Address); addr != "" {
					rcpts = append(rcpts, addr)
				}
			}
		}
	}
	return from, rcpts
}

func dedupeAddresses(addrs []string) []string {
	seen := make(map[string]struct{}, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, raw := range addrs {
		a := strings.TrimSpace(strings.Trim(raw, "<>"))
		if a == "" {
			continue
		}
		key := strings.ToLower(a)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, a)
	}
	return out
}

func submitSMTP(addr string, auth smtp.Auth, from string, rcpts []string, raw []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = c.Close() }()

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(raw); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

type sendmailPlainAuth struct {
	user string
	pass string
}

func (a *sendmailPlainAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (*sendmailPlainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge for AUTH PLAIN")
	}
	return nil, nil
}
