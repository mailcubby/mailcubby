package api

import "testing"

// SPEC 12.2, FR-074: active content, */xml, *+xml, multipart/*, and empty or
// syntactically invalid Content-Type values must be coerced to
// application/octet-stream, while safe media types return their normalised
// type/subtype with parameters stripped.
func TestSafeAttachmentContentType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		declared string
		want     string
	}{
		// +xml structured syntax suffix types.
		{name: "atom+xml", declared: "application/atom+xml", want: "application/octet-stream"},
		{name: "rss+xml", declared: "application/rss+xml", want: "application/octet-stream"},
		{name: "rdf+xml", declared: "application/rdf+xml", want: "application/octet-stream"},
		{name: "svg+xml", declared: "image/svg+xml", want: "application/octet-stream"},
		{name: "mathml+xml", declared: "application/mathml+xml", want: "application/octet-stream"},
		{name: "xhtml+xml", declared: "application/xhtml+xml", want: "application/octet-stream"},
		{name: "custom +xml with uppercase and params", declared: "Application/Vnd.Custom+XML; charset=utf-8", want: "application/octet-stream"},

		// */xml types.
		{name: "text/xml", declared: "text/xml", want: "application/octet-stream"},
		{name: "application/xml", declared: "application/xml", want: "application/octet-stream"},
		{name: "image/xml", declared: "image/xml", want: "application/octet-stream"},
		{name: "foo/xml", declared: "foo/xml", want: "application/octet-stream"},
		{name: "TEXT/XML with params", declared: "TEXT/XML; charset=utf-8", want: "application/octet-stream"},

		// Existing active content coercions.
		{name: "text/html", declared: "text/html", want: "application/octet-stream"},
		{name: "text/html uppercase with charset", declared: "TEXT/HTML; charset=utf-8", want: "application/octet-stream"},
		{name: "application/javascript", declared: "application/javascript", want: "application/octet-stream"},
		{name: "text/javascript", declared: "text/javascript", want: "application/octet-stream"},
		{name: "application/x-javascript", declared: "application/x-javascript", want: "application/octet-stream"},
		{name: "text/ecmascript", declared: "text/ecmascript", want: "application/octet-stream"},
		{name: "application/ecmascript", declared: "application/ecmascript", want: "application/octet-stream"},
		{name: "multipart/mixed", declared: "multipart/mixed; boundary=abc", want: "application/octet-stream"},
		{name: "multipart/x-mixed-replace", declared: "multipart/x-mixed-replace", want: "application/octet-stream"},

		// Empty and syntactically invalid Content-Type values.
		{name: "empty", declared: "", want: "application/octet-stream"},
		{name: "whitespace only", declared: "   ", want: "application/octet-stream"},
		{name: "missing slash", declared: "not-a-mime-type", want: "application/octet-stream"},
		{name: "missing subtype", declared: "application/", want: "application/octet-stream"},
		{name: "missing type", declared: "/pdf", want: "application/octet-stream"},
		{name: "extra slash", declared: "application/pdf/extra", want: "application/octet-stream"},
		{name: "malformed parameter on html", declared: "text/html; bad=;", want: "application/octet-stream"},
		{name: "malformed parameter on safe image", declared: "image/png; bad=;", want: "application/octet-stream"},
		{name: "unterminated quoted param", declared: `application/pdf; name="doc.pdf`, want: "application/octet-stream"},
		{name: "duplicate parameter", declared: "application/pdf; a=1; a=2", want: "application/octet-stream"},
		{name: "spaces around slash", declared: "image / png", want: "application/octet-stream"},
		{name: "crlf header injection", declared: "image/png\r\nX-Injected: 1", want: "application/octet-stream"},
		{name: "trailing crlf", declared: "image/png\r\n", want: "application/octet-stream"},
		{name: "crlf in quoted parameter", declared: "application/pdf; name=\"a\r\nb\"", want: "application/octet-stream"},
		{name: "nul byte", declared: "application/pdf\x00", want: "application/octet-stream"},
		{name: "del control char", declared: "application/pdf; name=\"a\x7fb\"", want: "application/octet-stream"},

		// Safe raster images (inline allowlist) and safe non-inline types.
		{name: "image/png", declared: "image/png", want: "image/png"},
		{name: "image/png with params", declared: `IMAGE/PNG; name="pic.png"`, want: "image/png"},
		{name: "image/jpeg", declared: "image/jpeg", want: "image/jpeg"},
		{name: "image/gif", declared: "image/gif", want: "image/gif"},
		{name: "image/webp", declared: "image/webp", want: "image/webp"},
		{name: "application/pdf", declared: "application/pdf", want: "application/pdf"},
		{name: "application/pdf with params", declared: `application/pdf; name="doc.pdf"`, want: "application/pdf"},
		{name: "text/plain with charset", declared: "text/plain; charset=utf-8", want: "text/plain"},
		{name: "application/json", declared: "application/json", want: "application/json"},
		{name: "non-xml subtype containing xml prefix", declared: "text/xmlish", want: "text/xmlish"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := safeAttachmentContentType(tc.declared); got != tc.want {
				t.Errorf("safeAttachmentContentType(%q) = %q, want %q", tc.declared, got, tc.want)
			}
		})
	}
}
