// Command gen fetches (or reads locally) the caniemail.com API dataset and
// writes a compacted, zstd-compressed snapshot to internal/compat/data/caniemail.json.zst.
//
// Usage:
//
//	go run ./internal/compat/cmd/gen
//	go run ./internal/compat/cmd/gen -in /path/to/data.json -out internal/compat/data/caniemail.json.zst
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	defaultSourceURL = "https://www.caniemail.com/api/data.json"
	defaultOutPath   = "internal/compat/data/caniemail.json.zst"
	maxDownloadBytes = 32 << 20 // 32 MiB safety cap
	httpTimeout      = 30 * time.Second
)

type clientSpec struct {
	ID         string `json:"id"`
	Family     string `json:"family"`
	FamilyName string `json:"family_name"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Weight     int    `json:"weight"`

	rawFamily   string
	rawPlatform string
}

// curatedClients defines the 18 current email clients tracked by Mailcubby,
// grouped into the 5 UI families (gmail, outlook, apple-mail, yahoo, other)
// with explicit Classic vs New Outlook distinction.
var curatedClients = []clientSpec{
	// Gmail
	{ID: "gmail.desktop-webmail", Family: "gmail", FamilyName: "Gmail", Name: "Gmail (Desktop Webmail)", Platform: "Desktop Webmail", Weight: 10, rawFamily: "gmail", rawPlatform: "desktop-webmail"},
	{ID: "gmail.ios", Family: "gmail", FamilyName: "Gmail", Name: "Gmail (iOS)", Platform: "iOS", Weight: 10, rawFamily: "gmail", rawPlatform: "ios"},
	{ID: "gmail.android", Family: "gmail", FamilyName: "Gmail", Name: "Gmail (Android)", Platform: "Android", Weight: 10, rawFamily: "gmail", rawPlatform: "android"},
	{ID: "gmail.mobile-webmail", Family: "gmail", FamilyName: "Gmail", Name: "Gmail (Mobile Webmail)", Platform: "Mobile Webmail", Weight: 3, rawFamily: "gmail", rawPlatform: "mobile-webmail"},

	// Outlook (Classic Word engine vs Outlook.com / New Outlook WebView2)
	{ID: "outlook.windows", Family: "outlook", FamilyName: "Outlook", Name: "Outlook Classic (Windows)", Platform: "Windows (Classic)", Weight: 8, rawFamily: "outlook", rawPlatform: "windows"},
	{ID: "outlook.outlook-com", Family: "outlook", FamilyName: "Outlook", Name: "Outlook.com / New Outlook", Platform: "Web / New Windows", Weight: 8, rawFamily: "outlook", rawPlatform: "outlook-com"},
	{ID: "outlook.macos", Family: "outlook", FamilyName: "Outlook", Name: "Outlook (macOS)", Platform: "macOS", Weight: 5, rawFamily: "outlook", rawPlatform: "macos"},
	{ID: "outlook.ios", Family: "outlook", FamilyName: "Outlook", Name: "Outlook (iOS)", Platform: "iOS", Weight: 6, rawFamily: "outlook", rawPlatform: "ios"},
	{ID: "outlook.android", Family: "outlook", FamilyName: "Outlook", Name: "Outlook (Android)", Platform: "Android", Weight: 6, rawFamily: "outlook", rawPlatform: "android"},

	// Apple Mail
	{ID: "apple-mail.macos", Family: "apple-mail", FamilyName: "Apple Mail", Name: "Apple Mail (macOS)", Platform: "macOS", Weight: 8, rawFamily: "apple-mail", rawPlatform: "macos"},
	{ID: "apple-mail.ios", Family: "apple-mail", FamilyName: "Apple Mail", Name: "Apple Mail (iOS)", Platform: "iOS", Weight: 12, rawFamily: "apple-mail", rawPlatform: "ios"},

	// Yahoo
	{ID: "yahoo.desktop-webmail", Family: "yahoo", FamilyName: "Yahoo! Mail", Name: "Yahoo! Mail (Desktop Webmail)", Platform: "Desktop Webmail", Weight: 5, rawFamily: "yahoo", rawPlatform: "desktop-webmail"},
	{ID: "yahoo.ios", Family: "yahoo", FamilyName: "Yahoo! Mail", Name: "Yahoo! Mail (iOS)", Platform: "iOS", Weight: 4, rawFamily: "yahoo", rawPlatform: "ios"},
	{ID: "yahoo.android", Family: "yahoo", FamilyName: "Yahoo! Mail", Name: "Yahoo! Mail (Android)", Platform: "Android", Weight: 4, rawFamily: "yahoo", rawPlatform: "android"},

	// Other
	{ID: "thunderbird.macos", Family: "other", FamilyName: "Other", Name: "Mozilla Thunderbird", Platform: "Desktop", Weight: 3, rawFamily: "thunderbird", rawPlatform: "macos"},
	{ID: "samsung-email.android", Family: "other", FamilyName: "Other", Name: "Samsung Email (Android)", Platform: "Android", Weight: 3, rawFamily: "samsung-email", rawPlatform: "android"},
	{ID: "protonmail.desktop-webmail", Family: "other", FamilyName: "Other", Name: "ProtonMail (Webmail)", Platform: "Webmail", Weight: 2, rawFamily: "protonmail", rawPlatform: "desktop-webmail"},
	{ID: "fastmail.desktop-webmail", Family: "other", FamilyName: "Other", Name: "Fastmail (Webmail)", Platform: "Webmail", Weight: 2, rawFamily: "fastmail", rawPlatform: "desktop-webmail"},
}

// latestVersionValue preserves JSON object insertion order so the last version
// entry in caniemail's chronological version map is selected deterministically.
type latestVersionValue struct {
	Latest string
}

func (l *latestVersionValue) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected object for version stats, got %v", tok)
	}
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			return err
		}
		if _, ok := kTok.(string); !ok {
			return fmt.Errorf("expected string version key, got %T", kTok)
		}
		var val string
		if err := dec.Decode(&val); err != nil {
			return err
		}
		l.Latest = val
	}
	_, err = dec.Token() // closing '}'
	return err
}

type rawDataset struct {
	APIVersion     string       `json:"api_version"`
	LastUpdateDate string       `json:"last_update_date"`
	Data           []rawFeature `json:"data"`
}

type rawFeature struct {
	Slug       string                                    `json:"slug"`
	Title      string                                    `json:"title"`
	URL        string                                    `json:"url"`
	Category   string                                    `json:"category"`
	Stats      map[string]map[string]*latestVersionValue `json:"stats"`
	NotesByNum map[string]string                         `json:"notes_by_num"`
}

type compactDataset struct {
	Source         string           `json:"source"`
	License        string           `json:"license"`
	APIVersion     string           `json:"api_version"`
	LastUpdateDate string           `json:"last_update_date"`
	Clients        []clientSpec     `json:"clients"`
	Features       []compactFeature `json:"features"`
}

type compactFeature struct {
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Category string            `json:"category"`
	URL      string            `json:"url"`
	Support  map[string]string `json:"support"`
	Notes    map[string]string `json:"notes,omitempty"`
}

func main() {
	inPath := flag.String("in", "", "read raw caniemail data.json from local file instead of downloading")
	sourceURL := flag.String("url", defaultSourceURL, "caniemail API JSON URL")
	outPath := flag.String("out", defaultOutPath, "output path for compacted zstd snapshot")
	flag.Parse()

	if err := run(*inPath, *sourceURL, *outPath); err != nil {
		fmt.Fprintf(os.Stderr, "compat/gen: %v\n", err)
		os.Exit(1)
	}
}

func run(inPath, sourceURL, outPath string) error {
	rawBytes, err := loadInput(inPath, sourceURL)
	if err != nil {
		return err
	}

	var raw rawDataset
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return fmt.Errorf("parse raw dataset: %w", err)
	}
	if len(raw.Data) == 0 {
		return errors.New("raw dataset contains no features")
	}

	features := make([]compactFeature, 0, len(raw.Data))
	for _, rf := range raw.Data {
		slug := strings.TrimSpace(rf.Slug)
		if slug == "" {
			continue
		}
		support := make(map[string]string, len(curatedClients))
		usedNotes := make(map[string]struct{})
		for _, c := range curatedClients {
			val := "u"
			if famMap, ok := rf.Stats[c.rawFamily]; ok {
				if lv, ok := famMap[c.rawPlatform]; ok && lv != nil && strings.TrimSpace(lv.Latest) != "" {
					val = strings.TrimSpace(lv.Latest)
				}
			}
			support[c.ID] = val
			for _, ref := range extractNoteRefs(val) {
				usedNotes[ref] = struct{}{}
			}
		}

		var notes map[string]string
		if len(usedNotes) > 0 && len(rf.NotesByNum) > 0 {
			notes = make(map[string]string, len(usedNotes))
			for ref := range usedNotes {
				if txt, ok := rf.NotesByNum[ref]; ok && strings.TrimSpace(txt) != "" {
					notes[ref] = strings.TrimSpace(txt)
				}
			}
			if len(notes) == 0 {
				notes = nil
			}
		}

		features = append(features, compactFeature{
			Slug:     slug,
			Title:    strings.TrimSpace(rf.Title),
			Category: strings.TrimSpace(rf.Category),
			URL:      strings.TrimSpace(rf.URL),
			Support:  support,
			Notes:    notes,
		})
	}

	sort.Slice(features, func(i, j int) bool {
		return features[i].Slug < features[j].Slug
	})

	out := compactDataset{
		Source:         "caniemail.com",
		License:        "MIT",
		APIVersion:     strings.TrimSpace(raw.APIVersion),
		LastUpdateDate: strings.TrimSpace(raw.LastUpdateDate),
		Clients:        curatedClients,
		Features:       features,
	}

	jsonBytes, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal compact dataset: %w", err)
	}

	var compressed bytes.Buffer
	enc, err := zstd.NewWriter(&compressed, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return fmt.Errorf("create zstd encoder: %w", err)
	}
	if _, err := enc.Write(jsonBytes); err != nil {
		_ = enc.Close()
		return fmt.Errorf("compress dataset: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close zstd encoder: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	// #nosec G306 -- non-secret embedded caniemail dataset checked into git.
	if err := os.WriteFile(outPath, compressed.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	fmt.Printf("wrote %s: %d features, %d clients, %d bytes JSON -> %d bytes zstd\n",
		outPath, len(features), len(curatedClients), len(jsonBytes), compressed.Len())
	return nil
}

func loadInput(inPath, sourceURL string) ([]byte, error) {
	if inPath != "" {
		// #nosec G304 -- CLI flag intentionally reads user-specified local snapshot file.
		data, err := os.ReadFile(inPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", inPath, err)
		}
		return data, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "mailcubby-compat-gen/1.0")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", sourceURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected HTTP %d", sourceURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("dataset exceeds %d bytes", maxDownloadBytes)
	}
	return data, nil
}

func extractNoteRefs(supportVal string) []string {
	fields := strings.Fields(supportVal)
	if len(fields) <= 1 {
		return nil
	}
	var refs []string
	for _, f := range fields[1:] {
		if num, ok := strings.CutPrefix(f, "#"); ok && num != "" {
			refs = append(refs, num)
		}
	}
	return refs
}
