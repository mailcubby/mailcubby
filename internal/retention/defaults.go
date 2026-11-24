// Package retention implements Mailcubby's background storage maintenance
// workers: retention sweeping (FR-095), orphan blob GC and incremental vacuum
// (FR-098), daily housekeeping (FR-099), and effective settings resolution (FR-102).
package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mailcubby/mailcubby/internal/config"
	"github.com/mailcubby/mailcubby/internal/store"
)

// Settings table keys defined in SPEC section 10.
const (
	SettingInstanceName         = "instance_name"
	SettingRetentionMaxAgeSec   = "retention_max_age_s"
	SettingRetentionMaxMessages = "retention_max_messages"
	SettingRetentionMaxBytes    = "retention_max_bytes"

	// SourceEnv indicates the effective value comes from environment configuration.
	SourceEnv = "env"
	// SourceOverride indicates the effective value comes from a DB settings override.
	SourceOverride = "override"
)

// Limits holds resolved retention thresholds for a project or instance default.
// A value of 0 disables that specific limit (SPEC FR-095).
type Limits struct {
	MaxAgeSec   int64
	MaxMessages int64
	MaxBytes    int64
}

// InstanceSettingsOverrides holds the raw DB overrides stored in the settings table.
// Nil fields inherit the environment variable default.
type InstanceSettingsOverrides struct {
	InstanceName         *string
	RetentionMaxAgeSec   *int64
	RetentionMaxMessages *int64
	RetentionMaxBytes    *int64
}

// InstanceSettingsEffective holds the active instance settings after merging DB
// overrides over environment variable defaults.
type InstanceSettingsEffective struct {
	InstanceName         string
	RetentionMaxAgeSec   int64
	RetentionMaxMessages int64
	RetentionMaxBytes    int64
}

// InstanceSettingsSource records whether each setting comes from "env" or "override".
type InstanceSettingsSource struct {
	InstanceName         string
	RetentionMaxAge      string
	RetentionMaxMessages string
	RetentionMaxBytes    string
}

// ResolvedInstanceSettings combines DB overrides, effective values, and their sources (FR-102).
type ResolvedInstanceSettings struct {
	Overrides InstanceSettingsOverrides
	Effective InstanceSettingsEffective
	Source    InstanceSettingsSource
}

// Limits returns the effective instance-level default retention limits.
func (r ResolvedInstanceSettings) Limits() Limits {
	return Limits{
		MaxAgeSec:   r.Effective.RetentionMaxAgeSec,
		MaxMessages: r.Effective.RetentionMaxMessages,
		MaxBytes:    r.Effective.RetentionMaxBytes,
	}
}

// ResolveInstanceSettings loads settings rows from st and merges them over cfg defaults.
func ResolveInstanceSettings(ctx context.Context, st *store.Store, cfg config.Config) (ResolvedInstanceSettings, error) {
	res := ResolvedInstanceSettings{
		Effective: InstanceSettingsEffective{
			InstanceName:         cfg.InstanceName,
			RetentionMaxAgeSec:   int64(cfg.RetentionMaxAge / time.Second),
			RetentionMaxMessages: cfg.RetentionMaxMessages,
			RetentionMaxBytes:    cfg.RetentionMaxBytes,
		},
		Source: InstanceSettingsSource{
			InstanceName:         SourceEnv,
			RetentionMaxAge:      SourceEnv,
			RetentionMaxMessages: SourceEnv,
			RetentionMaxBytes:    SourceEnv,
		},
	}

	if st == nil {
		return res, nil
	}

	rows, err := st.ListSettings(ctx)
	if err != nil {
		return ResolvedInstanceSettings{}, fmt.Errorf("retention: load instance settings: %w", err)
	}

	for _, row := range rows {
		switch row.Key {
		case SettingInstanceName:
			var name string
			if err := json.Unmarshal([]byte(row.Value), &name); err == nil && strings.TrimSpace(name) != "" {
				v := strings.TrimSpace(name)
				res.Overrides.InstanceName = &v
				res.Effective.InstanceName = v
				res.Source.InstanceName = SourceOverride
			}
		case SettingRetentionMaxAgeSec:
			var n int64
			if err := json.Unmarshal([]byte(row.Value), &n); err == nil && n >= 0 {
				v := n
				res.Overrides.RetentionMaxAgeSec = &v
				res.Effective.RetentionMaxAgeSec = v
				res.Source.RetentionMaxAge = SourceOverride
			}
		case SettingRetentionMaxMessages:
			var n int64
			if err := json.Unmarshal([]byte(row.Value), &n); err == nil && n >= 0 {
				v := n
				res.Overrides.RetentionMaxMessages = &v
				res.Effective.RetentionMaxMessages = v
				res.Source.RetentionMaxMessages = SourceOverride
			}
		case SettingRetentionMaxBytes:
			var n int64
			if err := json.Unmarshal([]byte(row.Value), &n); err == nil && n >= 0 {
				v := n
				res.Overrides.RetentionMaxBytes = &v
				res.Effective.RetentionMaxBytes = v
				res.Source.RetentionMaxBytes = SourceOverride
			}
		}
	}

	return res, nil
}

// ResolveProjectLimits resolves effective retention limits for a project:
// project override takes precedence over instance defaults; 0 disables a limit (FR-095).
func ResolveProjectLimits(ps store.ProjectSettings, defaults Limits) Limits {
	out := defaults
	if ps.RetentionMaxAgeSec != nil {
		out.MaxAgeSec = *ps.RetentionMaxAgeSec
	}
	if ps.RetentionMaxMessages != nil {
		out.MaxMessages = *ps.RetentionMaxMessages
	}
	if ps.RetentionMaxBytes != nil {
		out.MaxBytes = *ps.RetentionMaxBytes
	}
	return out
}
