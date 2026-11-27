package compat

import (
	"math"
	"sort"
)

// ClientSupport describes one email client's support level and notes for a feature.
type ClientSupport struct {
	ClientID string        `json:"client_id"`
	Status   SupportStatus `json:"status"`
	Notes    []string      `json:"notes,omitempty"`
}

// FeatureSummary holds per-feature counts across the curated clients.
type FeatureSummary struct {
	Supported   int `json:"supported"`
	Partial     int `json:"partial"`
	Unsupported int `json:"unsupported"`
	Unknown     int `json:"unknown"`
}

// FeatureResult holds compatibility details for one detected HTML/CSS feature.
type FeatureResult struct {
	Slug     string          `json:"slug"`
	Title    string          `json:"title"`
	Category string          `json:"category"`
	URL      string          `json:"url"`
	Count    int             `json:"count"`
	Summary  FeatureSummary  `json:"summary"`
	Support  []ClientSupport `json:"support"`
}

// ReportSummary aggregates compatibility scores and counts across all detected features.
type ReportSummary struct {
	SupportedPct   float64 `json:"supported_pct"`
	PartialPct     float64 `json:"partial_pct"`
	UnsupportedPct float64 `json:"unsupported_pct"`
	Supported      int     `json:"supported"`
	Partial        int     `json:"partial"`
	Unsupported    int     `json:"unsupported"`
	Unknown        int     `json:"unknown"`
	FeaturesCount  int     `json:"features_count"`
}

// Report is the complete HTML/CSS compatibility report for a message.
type Report struct {
	Score     float64         `json:"score"`
	Summary   ReportSummary   `json:"summary"`
	Dataset   DatasetMeta     `json:"dataset"`
	Truncated bool            `json:"truncated"`
	Clients   []ClientInfo    `json:"clients"`
	Features  []FeatureResult `json:"features"`
}

// buildReport scores detected feature slugs against ds using client weights and
// produces a deterministic, JSON-ready Report.
func buildReport(ds *Dataset, res analysisResult) Report {
	clients := make([]ClientInfo, len(ds.Clients))
	copy(clients, ds.Clients)

	features := make([]FeatureResult, 0, len(res.counts))
	var (
		totalSupported   int
		totalPartial     int
		totalUnsupported int
		totalUnknown     int

		weightedSupported   float64
		weightedPartial     float64
		weightedUnsupported float64
		totalWeight         float64
	)

	for slug, count := range res.counts {
		if count <= 0 {
			continue
		}
		fd, ok := ds.BySlug[slug]
		if !ok {
			continue
		}

		supportList := make([]ClientSupport, 0, len(ds.Clients))
		var fSum FeatureSummary

		for _, c := range ds.Clients {
			w := float64(c.Weight)
			if w <= 0 {
				w = 1
			}
			rawVal := fd.Support[c.ID]
			status, notes := parseSupportEntry(rawVal, fd.Notes)
			supportList = append(supportList, ClientSupport{
				ClientID: c.ID,
				Status:   status,
				Notes:    notes,
			})

			switch status {
			case StatusSupported:
				fSum.Supported++
				totalSupported++
				weightedSupported += w
				totalWeight += w
			case StatusPartial:
				fSum.Partial++
				totalPartial++
				weightedPartial += w
				totalWeight += w
			case StatusUnsupported:
				fSum.Unsupported++
				totalUnsupported++
				weightedUnsupported += w
				totalWeight += w
			default:
				fSum.Unknown++
				totalUnknown++
			}
		}

		features = append(features, FeatureResult{
			Slug:     fd.Slug,
			Title:    fd.Title,
			Category: fd.Category,
			URL:      fd.URL,
			Count:    count,
			Summary:  fSum,
			Support:  supportList,
		})
	}

	// Deterministic ordering: most problematic features first (unsupported desc,
	// then partial desc), breaking ties alphabetically by slug.
	sort.Slice(features, func(i, j int) bool {
		if features[i].Summary.Unsupported != features[j].Summary.Unsupported {
			return features[i].Summary.Unsupported > features[j].Summary.Unsupported
		}
		if features[i].Summary.Partial != features[j].Summary.Partial {
			return features[i].Summary.Partial > features[j].Summary.Partial
		}
		return features[i].Slug < features[j].Slug
	})

	var (
		supportedPct   = 100.0
		partialPct     = 0.0
		unsupportedPct = 0.0
		score          = 100.0
	)
	if totalWeight > 0 {
		supportedPct = round1((weightedSupported / totalWeight) * 100.0)
		partialPct = round1((weightedPartial / totalWeight) * 100.0)
		unsupportedPct = round1((weightedUnsupported / totalWeight) * 100.0)
		// Ensure rounded percentages stay bounded in [0, 100] and sum cleanly.
		diff := round1(100.0 - (supportedPct + partialPct + unsupportedPct))
		if diff != 0 {
			switch {
			case supportedPct >= partialPct && supportedPct >= unsupportedPct:
				supportedPct = round1(supportedPct + diff)
			case partialPct >= unsupportedPct:
				partialPct = round1(partialPct + diff)
			default:
				unsupportedPct = round1(unsupportedPct + diff)
			}
		}
		score = round1(((weightedSupported + 0.5*weightedPartial) / totalWeight) * 100.0)
	}

	return Report{
		Score: score,
		Summary: ReportSummary{
			SupportedPct:   supportedPct,
			PartialPct:     partialPct,
			UnsupportedPct: unsupportedPct,
			Supported:      totalSupported,
			Partial:        totalPartial,
			Unsupported:    totalUnsupported,
			Unknown:        totalUnknown,
			FeaturesCount:  len(features),
		},
		Dataset:   ds.Meta,
		Truncated: res.truncated,
		Clients:   clients,
		Features:  features,
	}
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
