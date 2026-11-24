package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	timestampFormat = "2006-01-02T15:04:05.000Z"
	maxJSONBodySize = 1 << 20 // 1 MiB (SPEC 8)
)

// ErrorCode identifies a machine-readable REST API error code (SPEC 8.1).
type ErrorCode string

const (
	// ErrCodeBadRequest indicates malformed request syntax or query parameters.
	ErrCodeBadRequest ErrorCode = "bad_request"
	// ErrCodeValidationFailed indicates field-level input validation failure (422).
	ErrCodeValidationFailed ErrorCode = "validation_failed"
	// ErrCodeUnauthorized indicates missing or invalid authentication credentials (401).
	ErrCodeUnauthorized ErrorCode = "unauthorized"
	// ErrCodeForbidden indicates insufficient permissions for the requested action (403).
	ErrCodeForbidden ErrorCode = "forbidden"
	// ErrCodeNotFound indicates the requested resource does not exist or is inaccessible (404).
	ErrCodeNotFound ErrorCode = "not_found"
	// ErrCodeMethodNotAllowed indicates an unsupported HTTP method on an existing route (405).
	ErrCodeMethodNotAllowed ErrorCode = "method_not_allowed"
	// ErrCodeConflict indicates a uniqueness or state conflict (409).
	ErrCodeConflict ErrorCode = "conflict"
	// ErrCodeLoginRequired indicates an invite requires logging into an existing account (409).
	ErrCodeLoginRequired ErrorCode = "login_required"
	// ErrCodeGone indicates an expired or revoked resource (410).
	ErrCodeGone ErrorCode = "gone"
	// ErrCodePayloadTooLarge indicates the request body exceeded the size limit (413).
	ErrCodePayloadTooLarge ErrorCode = "payload_too_large"
	// ErrCodeRateLimited indicates a rate limit was exceeded (429).
	ErrCodeRateLimited ErrorCode = "rate_limited"
	// ErrCodeInternal indicates an unexpected server error (500).
	ErrCodeInternal ErrorCode = "internal"
	// ErrCodeServiceUnavailable indicates the service is not ready (503).
	ErrCodeServiceUnavailable ErrorCode = "service_unavailable"
)

// Timestamp formats time.Time as RFC 3339 UTC with millisecond precision (SPEC 8.1).
type Timestamp time.Time

// MarshalJSON encodes Timestamp as a quoted RFC 3339 UTC millisecond string.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).UTC().Format(timestampFormat))
}

// Nullable distinguishes between an omitted JSON field, an explicit JSON null,
// and a non-null value in PATCH requests.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON decodes a nullable JSON field while recording presence.
func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	n.Set = true
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    ErrorCode         `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

func formatTimestampPtr(t *time.Time) *Timestamp {
	if t == nil {
		return nil
	}
	ts := Timestamp(*t)
	return &ts
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to encode response", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, code ErrorCode, message string, details map[string]string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func writeValidationError(w http.ResponseWriter, details map[string]string) {
	writeError(w, http.StatusUnprocessableEntity, ErrCodeValidationFailed, "validation failed", details)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "request body is required", nil)
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "request body exceeds maximum size", nil)
			return false
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "request body must not be empty", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("invalid JSON body: %v", err), nil)
		return false
	}

	// Reject trailing tokens after the first JSON value.
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "request body exceeds maximum size", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "request body must contain a single JSON object", nil)
		return false
	}
	return true
}

// decodeOptionalJSON decodes a JSON request body when present, allowing an
// empty body (e.g. a logged-in user accepting an invite without name/password).
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "request body exceeds maximum size", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, fmt.Sprintf("invalid JSON body: %v", err), nil)
		return false
	}

	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "request body exceeds maximum size", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "request body must contain a single JSON object", nil)
		return false
	}
	return true
}

// encodeMessageCursor encodes a message ULID into an opaque base64url cursor (SPEC 8.1).
func encodeMessageCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// decodeMessageCursor decodes and validates an opaque message cursor.
func decodeMessageCursor(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("empty cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("invalid cursor encoding: %w", err)
	}
	id := string(b)
	parsed, err := ulid.ParseStrict(id)
	if err != nil || parsed.String() != id {
		return "", errors.New("invalid cursor payload")
	}
	return id, nil
}
