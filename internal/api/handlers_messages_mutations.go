package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/mailcubby/mailcubby/internal/events"
	"github.com/mailcubby/mailcubby/internal/search"
	"github.com/mailcubby/mailcubby/internal/store"
)

const (
	defaultWaitTimeout = 30 * time.Second
	maxBulkMessageIDs  = 1000
)

type patchMessageRequest struct {
	Seen *bool `json:"seen"`
}

type bulkMessagesRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`
}

type affectedResponse struct {
	Affected int64 `json:"affected"`
}

func (s *Server) handlePatchMessage(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	msgID := r.PathValue("id")
	if msgID == "" {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
		return
	}

	var req patchMessageRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// TODO(phase-5): support tags updates in PATCH /messages/{id}.
	if req.Seen == nil {
		msg, err := s.store.GetMessageByID(r.Context(), pctx.Project.ID, msgID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
				return
			}
			s.log.Error("get message for patch failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"message_id", msgID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		writeJSON(w, http.StatusOK, toMessageSummary(msg))
		return
	}

	msg, proj, changed, err := s.store.SetMessageSeen(r.Context(), pctx.Project.ID, msgID, *req.Seen, s.clk.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
			return
		}
		s.log.Error("patch message seen failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msgID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	if changed {
		seenVal := *req.Seen
		s.publishMessageUpdated(proj, []string{msg.ID}, &seenVal)
		s.publishProjectUpdated(proj)
	}

	writeJSON(w, http.StatusOK, toMessageSummary(msg))
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	msgID := r.PathValue("id")
	if msgID == "" {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
		return
	}

	deleted, proj, err := s.store.DeleteMessage(r.Context(), pctx.Project.ID, msgID, s.clk.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "message not found", nil)
			return
		}
		s.log.Error("delete message failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", msgID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.removeBlobsBestEffort(r, pctx.Project.ID, []string{deleted.BlobPath})
	s.invalidateSparkline(pctx.Project.ID)
	s.publishMessageDeleted(proj, []string{deleted.ID}, false)
	s.publishProjectUpdated(proj)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBulkMessages(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	var req bulkMessagesRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	details := make(map[string]string)
	switch req.Action {
	case "delete", "seen", "unseen":
		// TODO(phase-5): support add_tags and remove_tags actions.
	default:
		details["action"] = "must be one of delete, seen, unseen"
	}

	switch {
	case len(req.IDs) == 0:
		details["ids"] = "must contain at least one message id"
	case len(req.IDs) > maxBulkMessageIDs:
		details["ids"] = "must contain at most 1000 message ids"
	default:
		for _, id := range req.IDs {
			if strings.TrimSpace(id) == "" {
				details["ids"] = "must not contain empty message ids"
				break
			}
		}
	}

	if len(details) > 0 {
		writeValidationError(w, details)
		return
	}

	now := s.clk.Now().UTC()
	switch req.Action {
	case "delete":
		deleted, proj, err := s.store.BulkDeleteMessages(r.Context(), pctx.Project.ID, req.IDs, now)
		if err != nil {
			s.log.Error("bulk delete messages failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		if len(deleted) > 0 {
			ids := make([]string, 0, len(deleted))
			paths := make([]string, 0, len(deleted))
			for _, d := range deleted {
				ids = append(ids, d.ID)
				paths = append(paths, d.BlobPath)
			}
			s.removeBlobsBestEffort(r, pctx.Project.ID, paths)
			s.invalidateSparkline(pctx.Project.ID)
			s.publishMessageDeleted(proj, ids, false)
			s.publishProjectUpdated(proj)
		}
		writeJSON(w, http.StatusOK, affectedResponse{Affected: int64(len(deleted))})

	case "seen", "unseen":
		seenVal := req.Action == "seen"
		changedIDs, proj, err := s.store.BulkSetMessagesSeen(r.Context(), pctx.Project.ID, req.IDs, seenVal, now)
		if err != nil {
			s.log.Error("bulk update messages seen failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"action", req.Action,
				"error", err,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		if len(changedIDs) > 0 {
			s.publishMessageUpdated(proj, changedIDs, &seenVal)
			s.publishProjectUpdated(proj)
		}
		writeJSON(w, http.StatusOK, affectedResponse{Affected: int64(len(changedIDs))})
	}
}

func (s *Server) handleDeleteAllMessages(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	rawQuery := strings.TrimSpace(r.URL.Query().Get("query"))
	if rawQuery != "" {
		parsedQuery, parseErr := search.Parse(rawQuery)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, parseErr.Error(), nil)
			return
		}
		if parsedQuery.IsEmpty() {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "query must contain at least one search term", nil)
			return
		}

		deleted, proj, delErr := search.DeleteMessagesByQuery(r.Context(), s.store, pctx.Project.ID, parsedQuery, s.clk.Now().UTC())
		if delErr != nil {
			s.log.Error("delete messages by query failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"error", delErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
		if len(deleted) > 0 {
			ids := make([]string, 0, len(deleted))
			paths := make([]string, 0, len(deleted))
			for _, d := range deleted {
				ids = append(ids, d.ID)
				paths = append(paths, d.BlobPath)
			}
			s.removeBlobsBestEffort(r, pctx.Project.ID, paths)
			s.invalidateSparkline(pctx.Project.ID)
			s.publishMessageDeleted(proj, ids, false)
			s.publishProjectUpdated(proj)
		}
		writeJSON(w, http.StatusOK, affectedResponse{Affected: int64(len(deleted))})
		return
	}

	affected, blobPaths, proj, err := s.store.DeleteAllMessages(r.Context(), pctx.Project.ID, s.clk.Now().UTC())
	if err != nil {
		s.log.Error("delete all messages failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	s.removeBlobsBestEffort(r, pctx.Project.ID, blobPaths)
	if affected > 0 {
		s.invalidateSparkline(pctx.Project.ID)
		s.publishMessageDeleted(proj, nil, true)
		s.publishProjectUpdated(proj)
	}

	writeJSON(w, http.StatusOK, affectedResponse{Affected: affected})
}

func (s *Server) handleSendTest(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}
	if s.ingestor == nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "ingestor is not configured", nil)
		return
	}

	ref, err := s.ingestor.SendTest(r.Context(), pctx.Project.ID, pctx.Project.Slug)
	if err != nil {
		s.log.Error("send test email failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	msg, err := s.store.GetMessageByID(r.Context(), pctx.Project.ID, ref.ID.String())
	if err != nil {
		s.log.Error("load test message after ingest failed",
			"request_id", RequestIDFromContext(r.Context()),
			"project_id", pctx.Project.ID,
			"message_id", ref.ID.String(),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusCreated, toMessageSummary(msg))
}

type waitSinceMode int

const (
	waitSinceNow waitSinceMode = iota
	waitSinceULID
	waitSinceTime
)

func (s *Server) handleWaitMessage(w http.ResponseWriter, r *http.Request) {
	pctx, ok := ProjectFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
		return
	}

	q := r.URL.Query()
	rawQuery := strings.TrimSpace(q.Get("query"))
	parsedQuery, parseErr := search.Parse(rawQuery)
	if parseErr != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, parseErr.Error(), nil)
		return
	}

	maxTimeout := s.cfg.APIWaitMaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = 120 * time.Second
	}
	timeout := min(defaultWaitTimeout, maxTimeout)
	if rawTimeout := strings.TrimSpace(q.Get("timeout")); rawTimeout != "" {
		parsed, err := time.ParseDuration(rawTimeout)
		if err != nil || parsed <= 0 || parsed > maxTimeout {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "timeout must be a positive duration not exceeding the configured maximum", nil)
			return
		}
		timeout = parsed
	}

	mode, sinceID, sinceTime, err := parseWaitSince(q.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
		return
	}

	// FR-078 / ARCHITECTURE 4.4: subscribe before querying the DB so no message can be missed.
	var (
		subCh <-chan events.Event
		unsub = func() {}
	)
	if s.bus != nil {
		var sub *events.Subscription
		sub, unsub = s.bus.Subscribe(events.ProjectFilter(pctx.Project.ID))
		subCh = sub.Events()
	}
	defer unsub()

	// Check existing messages if caller supplied an explicit ULID or RFC 3339 timestamp.
	switch mode {
	case waitSinceULID:
		var (
			existing  store.Message
			lookupErr error
		)
		if parsedQuery.IsEmpty() {
			existing, lookupErr = s.store.GetLatestMessageAfterID(r.Context(), pctx.Project.ID, sinceID)
		} else {
			matches, searchErr := search.Messages(r.Context(), s.store, pctx.Project.ID, parsedQuery, search.BuildOptions{
				AfterID: sinceID,
				Limit:   1,
			})
			switch {
			case searchErr != nil:
				lookupErr = searchErr
			case len(matches) == 0:
				lookupErr = store.ErrNotFound
			default:
				existing = matches[0]
			}
		}
		if lookupErr == nil {
			writeJSON(w, http.StatusOK, toMessageSummary(existing))
			return
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			s.log.Error("wait lookup after id failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"error", lookupErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	case waitSinceTime:
		var (
			existing  store.Message
			lookupErr error
		)
		if parsedQuery.IsEmpty() {
			existing, lookupErr = s.store.GetLatestMessageSinceTime(r.Context(), pctx.Project.ID, sinceTime)
		} else {
			ts := sinceTime
			matches, searchErr := search.Messages(r.Context(), s.store, pctx.Project.ID, parsedQuery, search.BuildOptions{
				SinceTime: &ts,
				Limit:     1,
			})
			switch {
			case searchErr != nil:
				lookupErr = searchErr
			case len(matches) == 0:
				lookupErr = store.ErrNotFound
			default:
				existing = matches[0]
			}
		}
		if lookupErr == nil {
			writeJSON(w, http.StatusOK, toMessageSummary(existing))
			return
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			s.log.Error("wait lookup since time failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", pctx.Project.ID,
				"error", lookupErr,
			)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
			return
		}
	}

	liftWriteDeadline(w)
	timer := s.clk.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCh:
			writeError(w, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "server is shutting down", nil)
			return
		case <-timer.C():
			w.WriteHeader(http.StatusNoContent)
			return
		case ev, open := <-subCh:
			if !open {
				writeError(w, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "event stream closed", nil)
				return
			}
			if ev.Type != events.TypeMessageCreated {
				continue
			}
			payload, valid := ev.Payload.(events.MessageCreatedPayload)
			if !valid {
				continue
			}
			if mode == waitSinceULID && payload.Message.ID <= sinceID {
				continue
			}
			if mode == waitSinceTime && payload.Message.ReceivedAt.Before(sinceTime) {
				continue
			}
			if !parsedQuery.IsEmpty() {
				matches, searchErr := search.Messages(r.Context(), s.store, pctx.Project.ID, parsedQuery, search.BuildOptions{
					MessageID: payload.Message.ID,
					Limit:     1,
				})
				if searchErr != nil {
					s.log.Error("wait search match failed",
						"request_id", RequestIDFromContext(r.Context()),
						"project_id", pctx.Project.ID,
						"message_id", payload.Message.ID,
						"error", searchErr,
					)
					writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
					return
				}
				if len(matches) == 0 {
					continue
				}
				writeJSON(w, http.StatusOK, toMessageSummary(matches[0]))
				return
			}
			msg, fetchErr := s.store.GetMessageByID(r.Context(), pctx.Project.ID, payload.Message.ID)
			if fetchErr != nil {
				if errors.Is(fetchErr, store.ErrNotFound) {
					continue
				}
				s.log.Error("wait load created message failed",
					"request_id", RequestIDFromContext(r.Context()),
					"project_id", pctx.Project.ID,
					"message_id", payload.Message.ID,
					"error", fetchErr,
				)
				writeError(w, http.StatusInternalServerError, ErrCodeInternal, "internal server error", nil)
				return
			}
			writeJSON(w, http.StatusOK, toMessageSummary(msg))
			return
		}
	}
}

func parseWaitSince(raw string) (waitSinceMode, string, time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "now") {
		return waitSinceNow, "", time.Time{}, nil
	}
	if parsed, err := ulid.ParseStrict(trimmed); err == nil && parsed.String() == trimmed {
		return waitSinceULID, trimmed, time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return waitSinceTime, "", ts.UTC(), nil
	}
	return waitSinceNow, "", time.Time{}, errors.New("since must be a valid ULID, an RFC 3339 timestamp, or 'now'")
}

func (s *Server) removeBlobsBestEffort(r *http.Request, projectID int64, paths []string) {
	if s.blobs == nil {
		return
	}
	for _, p := range paths {
		if err := s.blobs.Remove(p); err != nil {
			s.log.Warn("best-effort message blob cleanup failed",
				"request_id", RequestIDFromContext(r.Context()),
				"project_id", projectID,
				"blob_path", p,
				"error", err,
			)
		}
	}
}

func (s *Server) publishMessageUpdated(proj store.Project, ids []string, seen *bool) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{
		Type:      events.TypeMessageUpdated,
		ProjectID: proj.ID,
		Payload: events.MessageUpdatedPayload{
			Project: proj.Slug,
			IDs:     ids,
			Seen:    seen,
		},
	})
}

func (s *Server) publishMessageDeleted(proj store.Project, ids []string, all bool) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{
		Type:      events.TypeMessageDeleted,
		ProjectID: proj.ID,
		Payload: events.MessageDeletedPayload{
			Project: proj.Slug,
			IDs:     ids,
			All:     all,
		},
	})
}

func (s *Server) publishProjectUpdated(proj store.Project) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{
		Type:      events.TypeProjectUpdated,
		ProjectID: proj.ID,
		Payload: events.ProjectUpdatedPayload{
			Project:       proj.Slug,
			MessageCount:  proj.MessageCount,
			UnreadCount:   proj.UnreadCount,
			SizeBytes:     proj.SizeBytes,
			LastMessageAt: proj.LastMessageAt,
		},
	})
}
