package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const readyCheckTimeout = time.Second

type readyResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if s.listenersReady != nil && !s.listenersReady() {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable",
			Reason: "listeners are not ready",
		})
		return
	}

	checkCtx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
	defer cancel()

	if err := s.store.Ping(checkCtx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable",
			Reason: fmt.Sprintf("database unreachable: %v", err),
		})
		return
	}

	if err := s.store.MigrationsApplied(checkCtx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable",
			Reason: fmt.Sprintf("migrations not applied: %v", err),
		})
		return
	}

	if err := checkDirWritable(s.cfg.DataDir); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status: "unavailable",
			Reason: fmt.Sprintf("data dir not writable: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, readyResponse{
		Status: "ok",
	})
}

func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".readyz-*")
	if err != nil {
		return err
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
