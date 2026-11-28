package linkcheck

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

type memBlobOpener map[string][]byte

func (m memBlobOpener) Open(relPath string) (io.ReadCloser, error) {
	data, ok := m[relPath]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// FR-120: offline link extraction from HTML, plain text, and List-Unsubscribe headers,
// deduplication, source attribution, anchor text normalization, and static warnings.
func TestExtractLinksSourcesAndWarnings(t *testing.T) {
	t.Parallel()

	//nolint:gosec // G101: synthetic test URL exercising credentials_in_url detection
	rawHTML := `<!doctype html>
<html>
<body>
  <a id="top"></a>
  <a name="anchor">Section</a>
  <a href="https://example.com/welcome">  Welcome <b>aboard</b>  </a>
  <a href="https://example.com/welcome">Duplicate</a>
  <a href="http://insecure.example/page">Insecure link</a>
  <a href="https://user:pass@creds.example/login">Creds link</a>
  <a href="javascript:alert(1)">Run JS</a>
  <a href="  java	script:alert(2)">Obfuscated JS</a>
  <a href="data:text/html,<script>alert(1)</script>">Data URI</a>
  <a href="mailto:support@example.com">Email us</a>
  <a href="tel:+351910000000">Call us</a>
  <a href="#footer">Jump</a>
  <a href="">Empty href</a>
  <a>Missing href attr</a>
  <a href="https://example.com/logo"><img src="cid:logo" alt="Company Logo"></a>
  <map name="m">
    <area shape="rect" coords="0,0,10,10" href="https://example.com/area" alt="Hotspot">
    <area shape="rect" coords="0,0,5,5" alt="Broken area">
  </map>
</body>
</html>`

	rawText := `Hello!
Visit https://example.com/welcome.
Also see (https://en.wikipedia.org/wiki/Email_(protocol)), <https://example.com/angle>,
and ignore foohttps://not-a-link.example/x.
Unsubscribe: https://example.com/unsub?id=42!
`

	listUnsub := []string{
		"<mailto:unsub@example.com?subject=unsubscribe>, <https://example.com/unsub?id=42>",
	}

	got := Extract(rawHTML, rawText, listUnsub)
	if got.Truncated {
		t.Fatalf("Truncated = true, want false")
	}

	byURL := make(map[string]ExtractedLink, len(got.Links))
	for _, l := range got.Links {
		byURL[l.URL] = l
	}

	// Deduplicated across HTML (2x) and text (1x).
	welcome, ok := byURL["https://example.com/welcome"]
	if !ok {
		t.Fatalf("missing welcome link")
	}
	if welcome.Text != "Welcome aboard" {
		t.Errorf("welcome.Text = %q, want %q", welcome.Text, "Welcome aboard")
	}
	if welcome.Count != 3 {
		t.Errorf("welcome.Count = %d, want 3", welcome.Count)
	}
	if !slices.Equal(welcome.Sources, []Source{SourceHTML, SourceText}) {
		t.Errorf("welcome.Sources = %v, want [html text]", welcome.Sources)
	}
	if !welcome.Checkable || welcome.Scheme != "https" || len(welcome.Warnings) != 0 {
		t.Errorf("welcome unexpected meta: checkable=%v scheme=%q warnings=%v", welcome.Checkable, welcome.Scheme, welcome.Warnings)
	}

	// Plain-text balanced parens preserved; trailing comma stripped.
	wiki, ok := byURL["https://en.wikipedia.org/wiki/Email_(protocol)"]
	if !ok {
		t.Fatalf("missing balanced-paren Wikipedia URL; links=%+v", got.Links)
	}
	if !slices.Equal(wiki.Sources, []Source{SourceText}) || !wiki.Checkable {
		t.Errorf("wiki = %+v", wiki)
	}

	// Angle-bracketed plain-text URL extracted cleanly.
	if _, ok := byURL["https://example.com/angle"]; !ok {
		t.Errorf("missing angle-bracketed text URL")
	}
	// Embedded token without boundary ignored.
	if _, ok := byURL["https://not-a-link.example/x"]; ok {
		t.Errorf("should not extract URL attached to preceding alphanumeric token")
	}

	// Unsubscribe deduplicated across text and List-Unsubscribe header.
	unsub, ok := byURL["https://example.com/unsub?id=42"]
	if !ok {
		t.Fatalf("missing unsubscribe URL")
	}
	if unsub.Count != 2 || !slices.Equal(unsub.Sources, []Source{SourceText, SourceHeader}) {
		t.Errorf("unsub = %+v, want count=2 sources=[text header]", unsub)
	}

	// Header mailto: not checkable, no false credentials_in_url warning.
	unsubMailto, ok := byURL["mailto:unsub@example.com?subject=unsubscribe"]
	if !ok {
		t.Fatalf("missing header mailto link")
	}
	if unsubMailto.Checkable || unsubMailto.Scheme != "mailto" || len(unsubMailto.Warnings) != 0 {
		t.Errorf("unsubMailto = %+v", unsubMailto)
	}

	// Static warnings.
	if l := byURL["http://insecure.example/page"]; !l.Checkable || !slices.Equal(l.Warnings, []Warning{WarningHTTPInsecure}) {
		t.Errorf("insecure link = %+v", l)
	}
	if l := byURL["https://user:pass@creds.example/login"]; !l.Checkable || !slices.Equal(l.Warnings, []Warning{WarningCredentialsInURL}) {
		t.Errorf("creds link = %+v", l)
	}
	if l := byURL["javascript:alert(1)"]; l.Checkable || !slices.Equal(l.Warnings, []Warning{WarningJavaScriptURL}) {
		t.Errorf("js link = %+v", l)
	}
	if l := byURL["java\tscript:alert(2)"]; l.Checkable || !slices.Equal(l.Warnings, []Warning{WarningJavaScriptURL}) {
		t.Errorf("obfuscated js link = %+v", l)
	}
	if l := byURL["data:text/html,<script>alert(1)</script>"]; l.Checkable || !slices.Equal(l.Warnings, []Warning{WarningDataURL}) {
		t.Errorf("data link = %+v", l)
	}

	// Missing href aggregated (<a href="">, <a>Missing href attr</a>, <area> without href).
	// Named bookmarks (<a id="top">, <a name="anchor">) must NOT be counted as missing_href.
	missing, ok := byURL[""]
	if !ok {
		t.Fatalf("missing empty-href record")
	}
	if missing.Count != 3 || missing.Checkable || !slices.Equal(missing.Warnings, []Warning{WarningMissingHref}) {
		t.Errorf("missing href record = %+v, want count=3 checkable=false warnings=[missing_href]", missing)
	}

	// Image alt fallback inside <a> and <area alt>.
	if l := byURL["https://example.com/logo"]; l.Text != "Company Logo" {
		t.Errorf("img alt fallback = %q, want Company Logo", l.Text)
	}
	if l := byURL["https://example.com/area"]; l.Text != "Hotspot" {
		t.Errorf("area alt = %q, want Hotspot", l.Text)
	}

	// mailto/tel/fragment are non-checkable without warnings.
	for _, nonCheck := range []string{"mailto:support@example.com", "tel:+351910000000", "#footer"} {
		l, exists := byURL[nonCheck]
		if !exists {
			t.Errorf("missing %q", nonCheck)
			continue
		}
		if l.Checkable || len(l.Warnings) != 0 {
			t.Errorf("%q = %+v, want checkable=false warnings=[]", nonCheck, l)
		}
	}

	// CheckableCount equals exact count of checkable entries.
	wantCheckable := 0
	for _, l := range got.Links {
		if l.Checkable {
			wantCheckable++
		}
	}
	if got.CheckableCount != wantCheckable || got.Total != len(got.Links) {
		t.Errorf("totals mismatch: total=%d len=%d checkable=%d want=%d", got.Total, len(got.Links), got.CheckableCount, wantCheckable)
	}
}

// FR-120: HTML-only messages must not gain spurious "text" link sources from
// enmime's derived plain-text body, while List-Unsubscribe headers are extracted.
func TestExtractFromBlobHTMLOnlyNoSpuriousTextSource(t *testing.T) {
	t.Parallel()

	rawEML := "From: sender@example.test\r\n" +
		"To: rcpt@example.test\r\n" +
		"Subject: HTML Only\r\n" +
		"List-Unsubscribe: <https://example.com/unsub>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body><a href=\"https://example.com/cta\">Click</a></body></html>\r\n"

	res, err := ExtractFromBlob(t.Context(), memBlobOpener{"msg.eml": []byte(rawEML)}, "msg.eml")
	if err != nil {
		t.Fatalf("ExtractFromBlob: %v", err)
	}
	if len(res.Links) != 2 {
		t.Fatalf("len(Links) = %d, want 2; links=%+v", len(res.Links), res.Links)
	}
	if res.Links[0].URL != "https://example.com/cta" || !slices.Equal(res.Links[0].Sources, []Source{SourceHTML}) {
		t.Errorf("links[0] = %+v, want html-only source", res.Links[0])
	}
	if res.Links[1].URL != "https://example.com/unsub" || !slices.Equal(res.Links[1].Sources, []Source{SourceHeader}) {
		t.Errorf("links[1] = %+v, want header-only source", res.Links[1])
	}
}

func FuzzExtractLinks(f *testing.F) {
	f.Add(`<a href="https://example.com">Hi</a>`, `Check https://example.com.`, `<https://example.com/u>`)
	f.Add(`<a href="java	script:alert(1)">x</a><area href="">`, `(http://a.test/x(1)).`, `<mailto:a@b.test>`)
	f.Add(strings.Repeat(`<a href="https://example.com">x</a>`, 20), "", "")
	f.Fuzz(func(t *testing.T, rawHTML, rawText, hdr string) {
		res := Extract(rawHTML, rawText, []string{hdr})
		if res.Total != len(res.Links) {
			t.Fatalf("Total=%d != len(Links)=%d", res.Total, len(res.Links))
		}
		if res.Total > MaxExtractedLinks {
			t.Fatalf("Total=%d exceeded MaxExtractedLinks=%d", res.Total, MaxExtractedLinks)
		}
	})
}
