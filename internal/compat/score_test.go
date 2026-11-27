package compat

import (
	"math"
	"testing"
)

func TestBuildReportScoringEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("all-unknown feature defaults to 100% supported with unknown count preserved", func(t *testing.T) {
		t.Parallel()
		f := FeatureData{
			Slug:     "test-unknown",
			Title:    "Unknown feature",
			Category: "css",
			URL:      "https://www.caniemail.com/features/test-unknown/",
			Support:  map[string]string{"c1": "u", "c2": "u"},
		}
		ds := &Dataset{
			Clients: []ClientInfo{
				{ID: "c1", Family: "gmail", Weight: 5},
				{ID: "c2", Family: "outlook", Weight: 5},
			},
			Features: []FeatureData{f},
			BySlug:   map[string]*FeatureData{f.Slug: &f},
		}
		rep := buildReport(ds, analysisResult{counts: map[string]int{"test-unknown": 2}})
		if rep.Score != 100.0 || rep.Summary.SupportedPct != 100.0 || rep.Summary.Unknown != 2 {
			t.Fatalf("unexpected all-unknown report: score=%v summary=%+v", rep.Score, rep.Summary)
		}
	})

	t.Run("equal 3-way split adjusts rounding so percentages sum to exactly 100.0", func(t *testing.T) {
		t.Parallel()
		f := FeatureData{
			Slug:     "test-split",
			Title:    "Split feature",
			Category: "css",
			URL:      "https://www.caniemail.com/features/test-split/",
			Support:  map[string]string{"c1": "y", "c2": "a", "c3": "n"},
		}
		ds := &Dataset{
			Clients: []ClientInfo{
				{ID: "c1", Family: "gmail", Weight: 1},
				{ID: "c2", Family: "outlook", Weight: 1},
				{ID: "c3", Family: "yahoo", Weight: 1},
			},
			Features: []FeatureData{f},
			BySlug:   map[string]*FeatureData{f.Slug: &f},
		}
		rep := buildReport(ds, analysisResult{counts: map[string]int{"test-split": 1}})
		sum := round1(rep.Summary.SupportedPct + rep.Summary.PartialPct + rep.Summary.UnsupportedPct)
		if math.Abs(sum-100.0) > 1e-9 {
			t.Fatalf("percentages sum to %v, want 100.0 (%+v)", sum, rep.Summary)
		}
		if rep.Score != 50.0 {
			t.Fatalf("score = %v, want 50.0", rep.Score)
		}
	})
}
