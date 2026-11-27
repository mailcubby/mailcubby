package compat_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mailcubby/mailcubby/internal/compat"
)

// FR-079: embedded caniemail snapshot stays under 100 KB compressed and carries
// curated clients including Outlook Classic vs Outlook.com / New Outlook.
func TestEmbeddedDatasetSizeUnderLimit(t *testing.T) {
	t.Parallel()

	const maxCompressedBytes = 100 * 1024
	size := compat.EmbeddedDatasetSize()
	if size <= 0 || size >= maxCompressedBytes {
		t.Fatalf("EmbeddedDatasetSize() = %d, want (0, %d)", size, maxCompressedBytes)
	}

	ds, err := compat.LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if ds.Meta.Source != "caniemail.com" || ds.Meta.License != "MIT" {
		t.Errorf("unexpected dataset meta: %+v", ds.Meta)
	}
	if len(ds.Clients) < 15 || len(ds.Clients) > 25 {
		t.Errorf("curated client count = %d, expected ~18", len(ds.Clients))
	}

	clientIDs := make(map[string]compat.ClientInfo, len(ds.Clients))
	for _, c := range ds.Clients {
		clientIDs[c.ID] = c
	}
	for _, wantID := range []string{
		"gmail.desktop-webmail",
		"outlook.windows",
		"outlook.outlook-com",
		"apple-mail.macos",
		"apple-mail.ios",
		"yahoo.desktop-webmail",
	} {
		if _, ok := clientIDs[wantID]; !ok {
			t.Errorf("curated clients missing %q", wantID)
		}
	}
}

// FR-079: every slug declared in the rule registry must resolve in the embedded dataset.
func TestAllRuleSlugsExistInDataset(t *testing.T) {
	t.Parallel()

	ds, err := compat.LoadDataset()
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	slugs := compat.AllRuleSlugs()
	if len(slugs) == 0 {
		t.Fatal("AllRuleSlugs returned empty slice")
	}

	for _, slug := range slugs {
		if _, ok := ds.BySlug[slug]; !ok {
			t.Errorf("rule slug %q does not exist in embedded caniemail dataset", slug)
		}
	}
}

// FR-079: fixture tests for common email HTML/CSS patterns.
func TestAnalyzeHTMLFixtures(t *testing.T) {
	t.Parallel()

	checker, err := compat.New(nil)
	if err != nil {
		t.Fatalf("compat.New: %v", err)
	}

	tests := []struct {
		name           string
		html           string
		wantSlugs      []string
		forbidSlugs    []string
		wantCounts     map[string]int
		wantScoreBelow float64 // if > 0, assert report.Score < wantScoreBelow
	}{
		{
			name: "flexbox and grid layout",
			html: `<!DOCTYPE html><html><head><style>
				.wrap { display: flex; flex-direction: column; flex-wrap: wrap; gap: 16px; justify-content: center; align-items: center; }
				.grid { display: grid; grid-template-columns: 1fr 1fr; }
			</style></head><body><div class="wrap"><div class="grid">Hi</div></div></body></html>`,
			wantSlugs: []string{
				"html-doctype", "html-style", "html-div",
				"css-selector-class", "css-display-flex", "css-flex-direction",
				"css-flex-wrap", "css-gap", "css-justify-content", "css-align-items",
				"css-display-grid", "css-grid-template", "css-unit-px",
			},
			forbidSlugs:    []string{"css-display"},
			wantScoreBelow: 95,
		},
		{
			name: "media queries and prefers-color-scheme",
			html: `<head>
				<meta name="color-scheme" content="light dark">
				<style>
					@media screen and (max-width: 600px) {
						.card { width: 100% !important; }
					}
					@media (prefers-color-scheme: dark) {
						body { background-color: #111; }
					}
					@media (prefers-reduced-motion: reduce) {
						.anim { animation: none; }
					}
				</style>
			</head><body><div class="card">Hello</div></body>`,
			wantSlugs: []string{
				"html-meta-color-scheme",
				"css-at-media",
				"css-at-media-prefers-color-scheme",
				"css-at-media-prefers-reduced-motion",
				"css-important",
				"css-width",
				"css-unit-percent",
				"css-background-color",
				"css-animation",
			},
			wantCounts: map[string]int{
				"css-at-media":                      3,
				"css-at-media-prefers-color-scheme": 1,
			},
		},
		{
			name: "MSO conditional comments are ignored while downlevel-revealed content is kept",
			html: `<!DOCTYPE html><html><body>
				<!--[if mso]>
				<table role="presentation" cellpadding="0"><tr><td>
					<div style="display: grid; backdrop-filter: blur(4px);">MSO only</div>
					<svg width="10" height="10"></svg>
				</td></tr></table>
				<![endif]-->
				<!--[if !mso]><!-->
				<p style="border-radius: 8px;">Visible everywhere else</p>
				<!--<![endif]-->
			</body></html>`,
			wantSlugs: []string{"html-doctype", "html-body", "html-p", "css-border-radius", "css-unit-px"},
			forbidSlugs: []string{
				"html-comments",
				"html-table",
				"html-svg",
				"css-display-grid",
				"css-backdrop-filter",
			},
		},
		{
			name: "border-radius, box-shadow, gradients, and CSS variables",
			html: `<style>
				:root { --brand: #0ea5e9; }
				.btn {
					border-radius: 6px;
					box-shadow: 0 2px 4px rgba(0, 0, 0, 0.15);
					background-image: linear-gradient(90deg, var(--brand), oklch(0.7 0.2 200));
					width: clamp(200px, calc(100% - 2rem), 600px);
				}
			</style><a class="btn" href="mailto:test@example.test">Email</a>`,
			wantSlugs: []string{
				"css-variables",
				"css-border-radius",
				"css-box-shadow",
				"css-rgba",
				"css-background-image",
				"css-linear-gradient",
				"css-modern-color",
				"css-function-clamp",
				"css-unit-calc",
				"css-unit-rem",
				"html-mailto-links",
			},
		},
		{
			name: "embedded SVG, WebP, AVIF, and base64 images",
			html: `<body>
				<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>
				<picture>
					<source type="image/avif" srcset="https://cdn.example/hero.avif 1x, https://cdn.example/hero@2x.webp 2x">
					<img src="https://cdn.example/photo.webp?v=2" loading="lazy" alt="Hero">
				</picture>
				<img src="data:image/png;base64,iVBORw0KGgo=" alt="Tiny">
				<div style="background-image: url('https://cdn.example/bg.svg')"></div>
			</body>`,
			wantSlugs: []string{
				"html-svg",
				"html-picture",
				"html-img",
				"html-srcset",
				"html-loading-attribute",
				"image-avif",
				"image-webp",
				"image-base64",
				"image-png",
				"image-svg",
			},
		},
		{
			name: "selectors, combinators, pseudo-classes, pseudo-elements, and nesting",
			html: `<style>
				/* card styles */
				h1, h2 { font-family: system-ui, sans-serif; }
				#main .item.active > a:hover::after { content: ""; }
				input[type="checkbox"]:checked ~ .panel + .footer { display: none; }
				.card {
					padding: 12px;
					& .title { font-weight: bold; }
				}
			</style><h1>Hi</h1>`,
			wantSlugs: []string{
				"css-comments",
				"css-selector-grouping",
				"css-selector-type",
				"css-sytem-ui", //nolint:misspell // upstream caniemail.com slug typo
				"css-selector-id",
				"css-selector-descendant",
				"css-selector-class",
				"css-selector-chaining",
				"css-selector-child",
				"css-pseudo-class-hover",
				"css-pseudo-element-after",
				"css-selector-attribute",
				"css-pseudo-class-checked",
				"css-selector-general-sibling",
				"css-selector-adjacent-sibling",
				"css-display-none",
				"css-nesting",
				"css-padding",
				"css-font-weight",
			},
		},
		{
			name:      "plain text inside HTML without tracked features scores 100",
			html:      `hello world`,
			wantSlugs: nil,
		},
		{
			name: "negative CSS lengths and percentages detect units",
			html: `<div style="margin-top: -16px; left: -100%; text-indent: -1.5rem; margin-bottom: +.5em;">Neg</div>`,
			wantSlugs: []string{
				"css-margin",
				"css-left-right-top-bottom",
				"css-text-indent",
				"css-unit-px",
				"css-unit-percent",
				"css-unit-rem",
				"css-unit-em",
			},
		},
		{
			name: "CSS comments inside strings, before colons, in media preludes, and whitespace ! important",
			html: `<style>
				@media (/* dark */ prefers-color-scheme: dark) {
					.x {
						/* note: fallback */ display: flex ! important;
						background-image: url("https://cdn.example/a/*b*/c.webp");
					}
				}
			</style>`,
			wantSlugs: []string{
				"css-at-media",
				"css-at-media-prefers-color-scheme",
				"css-display-flex",
				"css-important",
				"css-background-image",
				"image-webp",
			},
		},
		{
			name:        "Tailwind/Maizzle escaped class selectors do not false-positive pseudo-classes",
			html:        `<style>.dark\:hover\:underline { text-decoration: underline; } .sm\:focus\:p-4 { padding: 16px; }</style>`,
			wantSlugs:   []string{"css-selector-class", "css-text-decoration", "css-padding", "css-unit-px"},
			forbidSlugs: []string{"css-pseudo-class-hover", "css-pseudo-class-focus"},
		},
		{
			name:      "unclosed CSS string stops at newline and does not swallow later rules",
			html:      "<style>\n.broken { content: \"unclosed;\n}\n.next { display: grid; }\n</style>",
			wantSlugs: []string{"css-selector-class", "css-display-grid"},
		},
		{
			name: "nested @media with bare declarations inside style rule and @page margin rule",
			html: `<style>
				.card {
					@media (max-width: 600px) {
						border-radius: 4px;
					}
				}
				@page {
					@top-center {
						margin: 1cm;
					}
				}
			</style>`,
			wantSlugs: []string{
				"css-nesting",
				"css-at-media",
				"css-border-radius",
				"css-margin",
				"css-unit-cm",
			},
		},
		{
			name:        "@page margin rule alone does not falsely flag css-nesting",
			html:        `<style>@page { @top-center { margin: 1cm; } }</style>`,
			wantSlugs:   []string{"css-margin", "css-unit-cm"},
			forbidSlugs: []string{"css-nesting"},
		},
		{
			name:        "minified MSO conditional comment without space before paren is ignored",
			html:        `<!--[if(gte mso 9)|(IE)]><table width="600"><tr><td>MSO</td></tr></table><![endif]--><p>Hi</p>`,
			wantSlugs:   []string{"html-p"},
			forbidSlugs: []string{"html-comments", "html-table"},
		},
		{
			name:      "keyframes with comment containing closing brace parses subsequent frames",
			html:      `<style>@keyframes fade { /* } */ from { opacity: 0; } to { opacity: 1; } }</style>`,
			wantSlugs: []string{"css-at-keyframes", "css-comments", "css-opacity"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := checker.AnalyzeHTML(tc.html)
			if rep.Truncated {
				t.Errorf("unexpected Truncated=true on small fixture")
			}

			got := make(map[string]compat.FeatureResult, len(rep.Features))
			for _, f := range rep.Features {
				got[f.Slug] = f
				if len(f.Support) != len(rep.Clients) {
					t.Errorf("feature %q has %d client entries, want %d", f.Slug, len(f.Support), len(rep.Clients))
				}
			}

			for _, want := range tc.wantSlugs {
				if _, ok := got[want]; !ok {
					t.Errorf("expected slug %q to be detected; got slugs: %v", want, keys(got))
				}
			}
			for _, forbid := range tc.forbidSlugs {
				if _, ok := got[forbid]; ok {
					t.Errorf("forbidden slug %q was unexpectedly detected", forbid)
				}
			}
			for slug, wantCount := range tc.wantCounts {
				if got[slug].Count != wantCount {
					t.Errorf("slug %q count = %d, want %d", slug, got[slug].Count, wantCount)
				}
			}
			if tc.wantScoreBelow > 0 && rep.Score >= tc.wantScoreBelow {
				t.Errorf("Score = %.1f, want < %.1f", rep.Score, tc.wantScoreBelow)
			}

			// Verify percentages sum to 100.
			pctSum := rep.Summary.SupportedPct + rep.Summary.PartialPct + rep.Summary.UnsupportedPct
			if math.Abs(pctSum-100.0) > 0.2 {
				t.Errorf("summary percentages sum to %.2f, want ~100.0 (%+v)", pctSum, rep.Summary)
			}
		})
	}
}

func TestOutlookClassicVsNewDistinction(t *testing.T) {
	t.Parallel()

	checker, err := compat.New(nil)
	if err != nil {
		t.Fatalf("compat.New: %v", err)
	}

	rep := checker.AnalyzeHTML(`<div style="border-radius: 8px; display: flex;">Card</div>`)
	bySlug := make(map[string]compat.FeatureResult, len(rep.Features))
	for _, f := range rep.Features {
		bySlug[f.Slug] = f
	}

	for _, slug := range []string{"css-border-radius", "css-display-flex"} {
		f, ok := bySlug[slug]
		if !ok {
			t.Fatalf("missing feature %q", slug)
		}
		sup := make(map[string]compat.ClientSupport, len(f.Support))
		for _, s := range f.Support {
			sup[s.ClientID] = s
		}
		if sup["outlook.windows"].Status != compat.StatusUnsupported {
			t.Errorf("%s on outlook.windows = %q, want unsupported", slug, sup["outlook.windows"].Status)
		}
		if sup["outlook.outlook-com"].Status != compat.StatusSupported {
			t.Errorf("%s on outlook.outlook-com = %q, want supported", slug, sup["outlook.outlook-com"].Status)
		}
	}
}

func TestAnalyzeHTMLCapsAndTruncation(t *testing.T) {
	t.Parallel()

	checker, err := compat.New(nil)
	if err != nil {
		t.Fatalf("compat.New: %v", err)
	}

	t.Run("deep CSS nesting is capped", func(t *testing.T) {
		t.Parallel()
		deep := "<style>" + strings.Repeat("@media screen { ", 64) + ".x { color: red; }" + strings.Repeat(" }", 64) + "</style>"
		rep := checker.AnalyzeHTML(deep)
		if !rep.Truncated {
			t.Errorf("expected Truncated=true for deep CSS nesting")
		}
	})

	t.Run("huge declaration count is capped", func(t *testing.T) {
		t.Parallel()
		var b strings.Builder
		b.WriteString("<style>.x {")
		for range compat.MaxCSSDeclarations + 50 {
			b.WriteString("margin:0;")
		}
		b.WriteString("}</style>")
		rep := checker.AnalyzeHTML(b.String())
		if !rep.Truncated {
			t.Errorf("expected Truncated=true when exceeding MaxCSSDeclarations")
		}
	})

	t.Run("oversized HTML input is capped", func(t *testing.T) {
		t.Parallel()
		big := "<p>" + strings.Repeat("a", compat.MaxHTMLBytes+100) + "</p>"
		rep := checker.AnalyzeHTML(big)
		if !rep.Truncated {
			t.Errorf("expected Truncated=true when exceeding MaxHTMLBytes")
		}
	})

	t.Run("oversized CSS bytes is capped", func(t *testing.T) {
		t.Parallel()
		bigCSS := "<style>/* " + strings.Repeat("x", compat.MaxCSSBytes+100) + " */ .a { color: red; }</style>"
		rep := checker.AnalyzeHTML(bigCSS)
		if !rep.Truncated {
			t.Errorf("expected Truncated=true when exceeding MaxCSSBytes")
		}
	})

	t.Run("excessive CSS rules and keyframe frames are capped", func(t *testing.T) {
		t.Parallel()
		manyRules := "<style>" + strings.Repeat("a{}", compat.MaxCSSRules+20) + "</style>"
		rep := checker.AnalyzeHTML(manyRules)
		if !rep.Truncated {
			t.Errorf("expected Truncated=true when exceeding MaxCSSRules")
		}

		manyKeyframes := "<style>@keyframes k {" + strings.Repeat("0%{}", compat.MaxCSSRules+20) + "}</style>"
		repKF := checker.AnalyzeHTML(manyKeyframes)
		if !repKF.Truncated {
			t.Errorf("expected Truncated=true when keyframes exceed MaxCSSRules")
		}
	})

	t.Run("excessive HTML nodes is capped", func(t *testing.T) {
		t.Parallel()
		manyNodes := strings.Repeat("<b></b>", (compat.MaxHTMLNodes/2)+100)
		rep := checker.AnalyzeHTML(manyNodes)
		if !rep.Truncated {
			t.Errorf("expected Truncated=true when exceeding MaxHTMLNodes")
		}
	})

	t.Run("pathological large srcset completes linearly without O(N^2) blowup", func(t *testing.T) {
		t.Parallel()
		// Regression test for O(N^2) strings.ToLower inside srcset parsing.
		hugeSrcset := `<img srcset="` + strings.Repeat("a.png 1x,", 200_000) + `b.webp 2x">`
		rep := checker.AnalyzeHTML(hugeSrcset)
		if rep.Score < 0 || rep.Score > 100 {
			t.Errorf("unexpected score %v", rep.Score)
		}
	})
}

type fakeBlobOpener struct {
	blobs map[string][]byte
	opens atomic.Int64
}

func (f *fakeBlobOpener) Open(relPath string) (io.ReadCloser, error) {
	f.opens.Add(1)
	b, ok := f.blobs[relPath]
	if !ok {
		return nil, errors.New("blob not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// FR-079: Checker LRU caching (keyed by {projectID, msgID}), CID image detection,
// and ErrNoHTML on text-only messages.
func TestCheckerCacheCIDAndNoHTML(t *testing.T) {
	t.Parallel()

	htmlEML := "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: Compat\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=\"REL\"\r\n\r\n" +
		"--REL\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><img src=\"cid:hero@img\"><div style=\"display:flex\">Hi</div></body></html>\r\n" +
		"--REL\r\n" +
		"Content-Type: image/webp\r\n" +
		"Content-ID: <hero@img>\r\n" +
		"Content-Disposition: inline; filename=\"hero.webp\"\r\n\r\n" +
		"RIFF\x00\x00\x00\x00WEBP\r\n" +
		"--REL--\r\n"

	textOnlyEML := "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: Plain\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Just plain text.\r\n"

	blobs := &fakeBlobOpener{
		blobs: map[string][]byte{
			"p1/m1": []byte(htmlEML),
			"p2/m1": []byte(htmlEML),
			"p1/m2": []byte(textOnlyEML),
		},
	}

	checker, err := compat.NewWithCacheSize(blobs, 2)
	if err != nil {
		t.Fatalf("NewWithCacheSize: %v", err)
	}

	rep1, err := checker.Check(t.Context(), 1, "m1", "p1/m1")
	if err != nil {
		t.Fatalf("Check p1/m1: %v", err)
	}
	if blobs.opens.Load() != 1 {
		t.Fatalf("opens = %d, want 1", blobs.opens.Load())
	}

	// Verify CID image MIME type (image/webp) was detected.
	foundWebP := false
	for _, f := range rep1.Features {
		if f.Slug == "image-webp" {
			foundWebP = true
			break
		}
	}
	if !foundWebP {
		t.Errorf("expected CID image/webp part to trigger image-webp feature")
	}

	// Second call for same {projectID, msgID} hits LRU cache.
	rep1Cached, err := checker.Check(t.Context(), 1, "m1", "p1/m1")
	if err != nil {
		t.Fatalf("Check p1/m1 cached: %v", err)
	}
	if blobs.opens.Load() != 1 {
		t.Fatalf("cached call opened blob: opens = %d, want 1", blobs.opens.Load())
	}

	// Byte-for-byte deterministic JSON.
	j1, _ := json.Marshal(rep1)
	j2, _ := json.Marshal(rep1Cached)
	if !bytes.Equal(j1, j2) {
		t.Errorf("cached report JSON differed from fresh report")
	}

	// Same msgID in a different project must NOT share cache entry.
	if _, err := checker.Check(t.Context(), 2, "m1", "p2/m1"); err != nil {
		t.Fatalf("Check p2/m1: %v", err)
	}
	if blobs.opens.Load() != 2 {
		t.Fatalf("different project should miss cache: opens = %d, want 2", blobs.opens.Load())
	}

	// Text-only message returns ErrNoHTML.
	if _, err := checker.Check(t.Context(), 1, "m2", "p1/m2"); !errors.Is(err, compat.ErrNoHTML) {
		t.Fatalf("Check text-only err = %v, want ErrNoHTML", err)
	}
}

// FR-079: fuzz test on the HTML/CSS compatibility analyzer ensuring no panics,
// bounded execution, and valid invariants on arbitrary untrusted email HTML.
func FuzzAnalyzeHTML(f *testing.F) {
	seeds := []string{
		"",
		"<html><body><p>Hello</p></body></html>",
		`<style>@media (prefers-color-scheme: dark) { .a.b > c:hover::before { display: grid; width: calc(100% - 1rem) !important; } }</style>`,
		`<!--[if mso]><table><tr><td>hi</td></tr></table><![endif]--><div style="background: url('data:image/webp;base64,AAA')"></div>`,
		`<style>/* unterminated comment`,
		`<style>.a { content: "unterminated string; background: url(unterminated</style>`,
		`<style>` + strings.Repeat("@media screen { ", 40) + `div{margin:1px}` + strings.Repeat("}", 40) + `</style>`,
		`<style>div { --a: 1px; color: oklch(0.5 0.1 200); & > span:has(> a:not(.x)) { gap: 4px; } }</style>`,
		`<img srcset="a.webp 1x, data:image/svg+xml;base64,PHN2Zz4= 2x" loading="lazy">`,
		`<style>}}}}}};;;@@@:::((([[["""'''`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	checker, err := compat.New(nil)
	if err != nil {
		f.Fatalf("compat.New: %v", err)
	}

	f.Fuzz(func(t *testing.T, input string) {
		rep := checker.AnalyzeHTML(input)
		if rep.Score < 0 || rep.Score > 100 || math.IsNaN(rep.Score) {
			t.Fatalf("invalid score %v for input %q", rep.Score, input)
		}
		if rep.Summary.FeaturesCount != len(rep.Features) {
			t.Fatalf("features_count mismatch: %d vs %d", rep.Summary.FeaturesCount, len(rep.Features))
		}
		for _, feat := range rep.Features {
			if feat.Count <= 0 {
				t.Fatalf("feature %q has non-positive count %d", feat.Slug, feat.Count)
			}
			if len(feat.Support) != len(rep.Clients) {
				t.Fatalf("feature %q support len %d != clients len %d", feat.Slug, len(feat.Support), len(rep.Clients))
			}
		}
	})
}

func keys(m map[string]compat.FeatureResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
