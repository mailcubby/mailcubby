package smtpd

import (
	"context"
	"errors"
	"strings"

	"github.com/emersion/go-smtp"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/ingest"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultProjectColor  = "slate"
	maxAutoProjectNameLn = 80
)

var (
	errAuthFailed         = &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}, Message: "Authentication credentials invalid"}
	errAuthLocked         = &smtp.SMTPError{Code: 454, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Temporary authentication failure"}
	errAuthRequired       = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0}, Message: "Authentication required"}
	errMailboxUnavailable = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "Mailbox unavailable"}
	errNoValidRecipients  = &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 5, 1}, Message: "No valid recipients"}
	errTempFailure        = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure, try again later"}
	errRateLimited        = &smtp.SMTPError{Code: 450, EnhancedCode: smtp.EnhancedCode{4, 7, 1}, Message: "Rate limit exceeded, try again later"}
	errQuotaExceeded      = &smtp.SMTPError{Code: 452, EnhancedCode: smtp.EnhancedCode{4, 3, 1}, Message: "Insufficient system storage"}
)

// AuthResult describes the outcome of SMTP AUTH on a session.
type AuthResult struct {
	Authenticated   bool
	Username        string
	ProjectID       int64
	ProjectSlug     string
	CredentialID    *int64
	CredentialLabel string
	PlusAddressTags bool
}

// ProjectLookup provides the store queries required by Resolve.
type ProjectLookup interface {
	EnsureProject(ctx context.Context, slug, name string) (store.Project, error)
	GetProjectBySlug(ctx context.Context, slug string) (store.Project, error)
}

// ParseRecipientRoute parses rcpt against inboxDomain per SPEC 7.1 and extracts
// any plus-address tag per FR-086.
func ParseRecipientRoute(rcpt, inboxDomain string) (slug string, plusTag string, routed bool) {
	addr := strings.TrimSpace(strings.Trim(rcpt, "<>"))
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || at >= len(addr)-1 {
		return "", "", false
	}

	localPart := addr[:at]
	domain := strings.ToLower(addr[at+1:])

	base, tag, hasPlus := strings.Cut(localPart, "+")
	if hasPlus && tag != "" {
		plusTag = tag
	}

	inbox := strings.ToLower(strings.TrimSpace(inboxDomain))
	if inbox == "" {
		return "", plusTag, false
	}

	// Form 1 (subdomain): <anything>@<s>.<INBOX_DOMAIN>
	suffix := "." + inbox
	if strings.HasSuffix(domain, suffix) {
		sub := strings.TrimSuffix(domain, suffix)
		if sub != "" && !strings.Contains(sub, ".") && config.ValidateSlug(sub) == nil {
			return sub, plusTag, true
		}
		return "", plusTag, false
	}

	// Form 2 (local-part): <s>@<INBOX_DOMAIN> or <s>+<t>@<INBOX_DOMAIN>
	if domain == inbox {
		cand := strings.ToLower(base)
		if config.ValidateSlug(cand) == nil {
			return cand, plusTag, true
		}
	}

	return "", plusTag, false
}

// Resolve maps an SMTP recipient to a destination project according to SPEC 7.2.
// Returned errors are always bare *smtp.SMTPError values per ARCHITECTURE 3.3.
func Resolve(
	ctx context.Context,
	mode config.Mode,
	auth AuthResult,
	rcpt string,
	inboxDomain string,
	defaultProject string,
	lookup ProjectLookup,
) (ingest.Target, *smtp.SMTPError) {
	routedSlug, plusTag, routed := ParseRecipientRoute(rcpt, inboxDomain)
	var plusTags []string
	if plusTag != "" {
		plusTags = []string{plusTag}
	}

	// Rule 1: AUTH succeeded overrides recipient routing completely.
	if auth.Authenticated {
		if mode == config.ModeLocal {
			slug := config.Slugify(auth.Username, defaultProject)
			name := autoProjectName(auth.Username, slug)
			proj, err := lookup.EnsureProject(ctx, slug, name)
			if err != nil {
				return ingest.Target{}, errTempFailure
			}
			return ingest.Target{
				ProjectID:       proj.ID,
				ProjectSlug:     proj.Slug,
				PlusTags:        plusTags,
				PlusAddressTags: proj.Settings.PlusAddressTags,
			}, nil
		}

		// Server mode authenticated credential (resolved during AUTH in Phase 8).
		return ingest.Target{
			ProjectID:       auth.ProjectID,
			ProjectSlug:     auth.ProjectSlug,
			CredentialID:    auth.CredentialID,
			CredentialLabel: auth.CredentialLabel,
			PlusTags:        plusTags,
			PlusAddressTags: auth.PlusAddressTags,
		}, nil
	}

	// Rule 2: No AUTH, RCPT routes to slug s.
	if routed {
		if mode == config.ModeLocal {
			proj, err := lookup.EnsureProject(ctx, routedSlug, autoProjectName(routedSlug, routedSlug))
			if err != nil {
				return ingest.Target{}, errTempFailure
			}
			return ingest.Target{
				ProjectID:       proj.ID,
				ProjectSlug:     proj.Slug,
				PlusTags:        plusTags,
				PlusAddressTags: proj.Settings.PlusAddressTags,
			}, nil
		}

		proj, err := lookup.GetProjectBySlug(ctx, routedSlug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ingest.Target{}, errMailboxUnavailable
			}
			return ingest.Target{}, errTempFailure
		}
		if !proj.Settings.RecipientRoutingEnabled {
			return ingest.Target{}, errMailboxUnavailable
		}
		return ingest.Target{
			ProjectID:       proj.ID,
			ProjectSlug:     proj.Slug,
			PlusTags:        plusTags,
			PlusAddressTags: proj.Settings.PlusAddressTags,
		}, nil
	}

	// Rules 3 & 4: No AUTH, RCPT does not route (or inboxDomain empty).
	if mode == config.ModeLocal {
		proj, err := lookup.EnsureProject(ctx, defaultProject, autoProjectName(defaultProject, defaultProject))
		if err != nil {
			return ingest.Target{}, errTempFailure
		}
		return ingest.Target{
			ProjectID:       proj.ID,
			ProjectSlug:     proj.Slug,
			PlusTags:        plusTags,
			PlusAddressTags: proj.Settings.PlusAddressTags,
		}, nil
	}

	return ingest.Target{}, errMailboxUnavailable
}

func autoProjectName(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = fallback
	}
	runes := []rune(trimmed)
	if len(runes) > maxAutoProjectNameLn {
		return string(runes[:maxAutoProjectNameLn])
	}
	return trimmed
}
