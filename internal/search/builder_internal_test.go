package search

import "testing"

// TestFilterShape covers the no-op detection behind the filtered delete-all
// guard (FR-076, FR-081, FR-082).
func TestFilterShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name               string
		query              string
		wantNarrows        bool
		wantExclusionsOnly bool
	}{
		{name: "negated punctuation phrase", query: `-"!"`},
		{name: "negated punctuation word", query: `-!`},
		{name: "negated star", query: `-*`},
		{name: "several negated no-op terms", query: `-"!" -"?" -from:! -subject:"..."`},
		{name: "negated unicode61 separator letter", query: "-ᦰ"},
		{name: "negated vedic sign", query: "-ᳲᳳ"},
		{name: "positive no-op is impossible not empty", query: `"!"`, wantNarrows: true},
		{name: "negated word", query: `-hello`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated astral letter tokenized by unicode61", query: "-\U0001E030", wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated from", query: `-from:alice`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated tag", query: `-tag:invoice`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated credential", query: `-credential:staging`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated credential with empty value is text", query: `-credential:`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated no-op plus negated word", query: `-"!" -hello`, wantNarrows: true, wantExclusionsOnly: true},
		{name: "negated no-op plus positive word", query: `-"!" hello`, wantNarrows: true},
		{name: "negated is operator is not an exclusion", query: `-is:read`, wantNarrows: true},
		{name: "negated date is not an exclusion", query: `-before:2026-01-01`, wantNarrows: true},
		{name: "positive operator", query: `has:attachment`, wantNarrows: true},
		{name: "positive credential", query: `credential:staging`, wantNarrows: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.query, err)
			}
			if q.IsEmpty() {
				t.Fatalf("Parse(%q) produced no terms; the case must exercise the builder", tc.query)
			}
			narrows, exclusionsOnly := filterShape(q)
			if narrows != tc.wantNarrows || exclusionsOnly != tc.wantExclusionsOnly {
				t.Fatalf("filterShape(%q) = (%v, %v), want (%v, %v)", tc.query, narrows, exclusionsOnly, tc.wantNarrows, tc.wantExclusionsOnly)
			}
		})
	}
}

// TestSanitizeFTSInputMatchesUnicode61 checks that code points Go treats as
// letters but SQLite's unicode61 tokenizer (Unicode 6.1) treats as separators
// never count as FTS tokens (FR-080).
func TestSanitizeFTSInputMatchesUnicode61(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: "ᦰᧀᧈᧉ", want: ""},
		{in: "ᳲᳳ", want: ""},
		{in: "abcᦰ", want: "abc"},
		{in: "ᧁ", want: "ᧁ"}, // New Tai Lue final consonant: a letter in 6.1 too.
		{in: "\U0001E030", want: "\U0001E030"},
		{in: "!joão!", want: "joão"},
	}
	for _, tc := range cases {
		if got := sanitizeFTSInput(tc.in); got != tc.want {
			t.Errorf("sanitizeFTSInput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
