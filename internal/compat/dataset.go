// Package compat performs offline email HTML/CSS compatibility analysis against
// an embedded, zstd-compressed snapshot of the caniemail.com dataset (MIT).
package compat

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

//go:embed data/caniemail.json.zst
var embeddedDatasetZst []byte

// SupportStatus describes how an email client supports a detected HTML/CSS feature.
type SupportStatus string

const (
	// StatusSupported means the client supports the feature ("y" in caniemail).
	StatusSupported SupportStatus = "supported"
	// StatusPartial means the client has partial or buggy support ("a" in caniemail).
	StatusPartial SupportStatus = "partial"
	// StatusUnsupported means the client does not support the feature ("n" in caniemail).
	StatusUnsupported SupportStatus = "unsupported"
	// StatusUnknown means support is unknown or untested ("u" in caniemail).
	StatusUnknown SupportStatus = "unknown"
)

// ClientInfo describes one curated email client in the compatibility matrix.
type ClientInfo struct {
	ID         string `json:"id"`
	Family     string `json:"family"`
	FamilyName string `json:"family_name"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Weight     int    `json:"weight"`
}

// DatasetMeta describes provenance and freshness of the embedded caniemail snapshot.
type DatasetMeta struct {
	Source         string `json:"source"`
	License        string `json:"license"`
	APIVersion     string `json:"api_version"`
	LastUpdateDate string `json:"last_update_date"`
}

// FeatureData holds one compacted feature entry from the embedded dataset.
type FeatureData struct {
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Category string            `json:"category"`
	URL      string            `json:"url"`
	Support  map[string]string `json:"support"`
	Notes    map[string]string `json:"notes,omitempty"`
}

type rawCompactDataset struct {
	Source         string        `json:"source"`
	License        string        `json:"license"`
	APIVersion     string        `json:"api_version"`
	LastUpdateDate string        `json:"last_update_date"`
	Clients        []ClientInfo  `json:"clients"`
	Features       []FeatureData `json:"features"`
}

// Dataset is the immutable, decompressed caniemail dataset held by a Checker.
type Dataset struct {
	Meta     DatasetMeta
	Clients  []ClientInfo
	Features []FeatureData
	BySlug   map[string]*FeatureData
}

// EmbeddedDatasetSize returns the compressed byte length of the embedded dataset.
func EmbeddedDatasetSize() int {
	return len(embeddedDatasetZst)
}

// LoadDataset decompresses and parses the embedded caniemail.json.zst snapshot
// into an immutable Dataset instance.
func LoadDataset() (*Dataset, error) {
	dec, err := zstd.NewReader(bytes.NewReader(embeddedDatasetZst), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("compat: create zstd reader: %w", err)
	}
	defer dec.Close()

	// Cap decompressed read at 8 MiB as a safety guard against corrupted embeds.
	const maxDecompressedBytes = 8 << 20
	rawJSON, err := io.ReadAll(io.LimitReader(dec, maxDecompressedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("compat: decompress dataset: %w", err)
	}
	if len(rawJSON) > maxDecompressedBytes {
		return nil, fmt.Errorf("compat: decompressed dataset exceeds %d bytes", maxDecompressedBytes)
	}

	var raw rawCompactDataset
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, fmt.Errorf("compat: decode dataset: %w", err)
	}
	if len(raw.Clients) == 0 || len(raw.Features) == 0 {
		return nil, fmt.Errorf("compat: embedded dataset is empty")
	}

	bySlug := make(map[string]*FeatureData, len(raw.Features))
	for i := range raw.Features {
		f := &raw.Features[i]
		bySlug[f.Slug] = f
	}

	return &Dataset{
		Meta: DatasetMeta{
			Source:         raw.Source,
			License:        raw.License,
			APIVersion:     raw.APIVersion,
			LastUpdateDate: raw.LastUpdateDate,
		},
		Clients:  raw.Clients,
		Features: raw.Features,
		BySlug:   bySlug,
	}, nil
}

// parseSupportEntry parses a caniemail support string such as "y", "a #1 #2",
// "n #1", or "u" into a SupportStatus and resolved note texts.
func parseSupportEntry(raw string, notesByNum map[string]string) (SupportStatus, []string) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return StatusUnknown, nil
	}

	var status SupportStatus
	switch fields[0] {
	case "y":
		status = StatusSupported
	case "a":
		status = StatusPartial
	case "n":
		status = StatusUnsupported
	default:
		status = StatusUnknown
	}

	var notes []string
	if len(fields) > 1 && len(notesByNum) > 0 {
		seen := make(map[string]struct{}, len(fields)-1)
		for _, tok := range fields[1:] {
			num, ok := strings.CutPrefix(tok, "#")
			if !ok || num == "" {
				continue
			}
			if _, dup := seen[num]; dup {
				continue
			}
			seen[num] = struct{}{}
			if text, exists := notesByNum[num]; exists && text != "" {
				notes = append(notes, text)
			}
		}
	}
	return status, notes
}
