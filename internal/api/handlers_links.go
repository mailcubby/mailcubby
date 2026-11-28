package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/mailcubby/mailcubby/internal/auth"
	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/linkcheck"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
)

type checkMessageLinksRequest struct {
	URLs *[]string `json:"urls"`
}

type linkCheckResponse struct {
	CheckedAt Timestamp               `json:"checked_at"`
	Summary   linkcheck.CheckSummary  `json:"summary"`
	Results   []linkcheck.CheckResult `json:"results"`
}

// handleGetMessageLinks extracts and deduplicates links offline from the message
// blob (HTML, plain text, List-Unsubscribe headers) per ADR 0010 and FR-120.
func (s *Server) handleGetMessageLinks(w http.ResponseWriter, r *http.Request) {
	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}
	if s.linkChecker == nil {
		s.log.Error("link checker is not initialized",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	extracted, err := s.linkChecker.Extract(r.Context(), msg.BlobPath)
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		s.log.Error("link extraction failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, extracted)
}

// handleCheckMessageLinks performs explicit user-triggered active HTTP link
// checking for a message under ADR 0010 SSRF protections, per-message URL
// allowlisting, and the server-mode 10/m rate limit (FR-120).
func (s *Server) handleCheckMessageLinks(w http.ResponseWriter, r *http.Request) {
	pctx, msg, ok := s.loadProjectMessage(w, r)
	if !ok {
		return
	}
	if !s.cfg.LinkCheckEnabled {
		writeError(w, http.StatusForbidden, ErrCodeFeatureDisabled, "link checking is disabled on this instance", nil)
		return
	}
	if s.linkChecker == nil {
		s.log.Error("link checker is not initialized",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var req checkMessageLinksRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}

	// Fail fast on an already-exhausted server-mode 10/m bucket before decompressing
	// and parsing the message blob, without consuming a slot on malformed requests.
	var rlKey string
	if s.cfg.Mode == config.ModeServer && s.linkCheckLimiter != nil {
		rlKey = linkCheckRateLimitKey(PrincipalFromContext(r.Context()))
		if rlKey != "" {
			if allowed, retryAfter := s.linkCheckLimiter.Check(rlKey); !allowed {
				sec := ratelimit.RetryAfterSeconds(retryAfter)
				w.Header().Set("Retry-After", strconv.Itoa(sec))
				writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "rate limit exceeded, try again later", map[string]string{
					"retry_after": strconv.Itoa(sec),
				})
				return
			}
		}
	}

	extracted, err := s.linkChecker.Extract(r.Context(), msg.BlobPath)
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		s.log.Error("link extraction for check failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msg.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	var targetURLs []string
	if req.URLs != nil {
		if len(*req.URLs) == 0 {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "urls must not be empty when specified", nil)
			return
		}
		allowedSet := make(map[string]struct{}, len(extracted.Links))
		for _, l := range extracted.Links {
			if l.URL != "" {
				allowedSet[l.URL] = struct{}{}
			}
		}
		for _, u := range *req.URLs {
			if _, found := allowedSet[u]; !found {
				// ADR 0010: arbitrary caller-supplied URLs return 400; never log URLs.
				writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "one or more requested URLs were not found in this message", nil)
				return
			}
		}
		targetURLs = *req.URLs
	} else {
		targetURLs = make([]string, 0, extracted.CheckableCount)
		for _, l := range extracted.Links {
			if l.Checkable && l.URL != "" {
				targetURLs = append(targetURLs, l.URL)
			}
		}
	}

	if rlKey != "" {
		if allowed, retryAfter := s.linkCheckLimiter.Allow(rlKey); !allowed {
			sec := ratelimit.RetryAfterSeconds(retryAfter)
			w.Header().Set("Retry-After", strconv.Itoa(sec))
			writeError(w, http.StatusTooManyRequests, ErrCodeRateLimited, "rate limit exceeded, try again later", map[string]string{
				"retry_after": strconv.Itoa(sec),
			})
			return
		}
	}

	report := s.linkChecker.Check(r.Context(), pctx.Project.ID, msg.ID, targetURLs)
	if r.Context().Err() != nil {
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, linkCheckResponse{
		CheckedAt: Timestamp(report.CheckedAt),
		Summary:   report.Summary,
		Results:   report.Results,
	})
}

func linkCheckRateLimitKey(p Principal) string {
	switch p.Kind {
	case auth.PrincipalUser:
		if p.SessionID != "" {
			return "session:" + p.SessionID
		}
	case auth.PrincipalToken:
		if p.TokenID > 0 {
			return "token:" + strconv.FormatInt(p.TokenID, 10)
		}
	}
	return ""
}
