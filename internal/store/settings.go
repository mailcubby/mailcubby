package store

import (
	"context"
	"fmt"
	"time"

	"github.com/mailcubby/mailcubby/internal/store/sqlc"
)

// Setting represents one key/value entry in the settings table (SPEC 10).
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// GetSetting fetches a single setting by key.
func (s *Store) GetSetting(ctx context.Context, key string) (Setting, error) {
	return getSetting(ctx, s.readQ, key)
}

// GetSetting fetches a single setting by key inside tx.
func (t *Tx) GetSetting(ctx context.Context, key string) (Setting, error) {
	return getSetting(ctx, t.q, key)
}

func getSetting(ctx context.Context, q *sqlc.Queries, key string) (Setting, error) {
	row, err := q.GetSetting(ctx, key)
	if err != nil {
		return Setting{}, fmt.Errorf("store: get setting %q: %w", key, mapError(err))
	}
	return settingFromRow(row), nil
}

// SetSetting inserts or updates a setting key/value pair.
func (s *Store) SetSetting(ctx context.Context, key, value string, now time.Time) error {
	return setSetting(ctx, s.writeQ, key, value, now)
}

// SetSetting inserts or updates a setting key/value pair inside tx.
func (t *Tx) SetSetting(ctx context.Context, key, value string, now time.Time) error {
	return setSetting(ctx, t.q, key, value, now)
}

func setSetting(ctx context.Context, q *sqlc.Queries, key, value string, now time.Time) error {
	if err := q.SetSetting(ctx, sqlc.SetSettingParams{
		Key:       key,
		Value:     value,
		UpdatedAt: now.UTC().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, mapError(err))
	}
	return nil
}

// DeleteSetting deletes a setting by key, returning ErrNotFound if it does not exist.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	return deleteSetting(ctx, s.writeQ, key)
}

// DeleteSetting deletes a setting by key inside tx.
func (t *Tx) DeleteSetting(ctx context.Context, key string) error {
	return deleteSetting(ctx, t.q, key)
}

func deleteSetting(ctx context.Context, q *sqlc.Queries, key string) error {
	affected, err := q.DeleteSetting(ctx, key)
	if err != nil {
		return fmt.Errorf("store: delete setting %q: %w", key, mapError(err))
	}
	if affected == 0 {
		return fmt.Errorf("store: delete setting %q: %w", key, ErrNotFound)
	}
	return nil
}

// ListSettings returns all settings ordered by key ascending.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.readQ.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list settings: %w", mapError(err))
	}
	out := make([]Setting, 0, len(rows))
	for _, r := range rows {
		out = append(out, settingFromRow(r))
	}
	return out, nil
}

func settingFromRow(r sqlc.Setting) Setting {
	return Setting{
		Key:       r.Key,
		Value:     r.Value,
		UpdatedAt: time.UnixMilli(r.UpdatedAt).UTC(),
	}
}
