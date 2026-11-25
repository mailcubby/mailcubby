package render_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/mailcubby/mailcubby/internal/render"
)

// FR-072: Result.ReferencesCID must report exactly the parts the rewrite inlined.
// The oracle is independent of ReferencedCIDs: a part is referenced if and only
// if the rewritten HTML contains the base64 encoding of its content.
func TestReferencesCIDMatchesRewrite(t *testing.T) {
	t.Parallel()

	part := func(n int) render.InlinePart {
		// Distinct content per part so base64 substrings never collide.
		return render.InlinePart{
			ContentType: "image/png",
			Content:     bytes.Repeat([]byte(fmt.Sprintf("part-%02d|", n)), 8),
		}
	}

	cases := []struct {
		name           string
		html           string
		cids           map[string]render.InlinePart
		maxInlineBytes int
		// contentIDs are the Content-ID values as ingest stores them (angle
		// brackets trimmed, original case), keyed by the cids map key.
		contentIDs map[string]string
		want       map[string]bool
	}{
		{
			name:       "img src with case difference",
			html:       `<img src="CID:Logo@Example.TEST">`,
			cids:       map[string]render.InlinePart{"<logo@example.test>": part(1)},
			contentIDs: map[string]string{"<logo@example.test>": "logo@example.test"},
			want:       map[string]bool{"<logo@example.test>": true},
		},
		{
			name:       "angle brackets inside the reference",
			html:       `<img src="cid:<Banner@Example.test>">`,
			cids:       map[string]render.InlinePart{"<banner@example.test>": part(2)},
			contentIDs: map[string]string{"<banner@example.test>": "Banner@Example.test"},
			want:       map[string]bool{"<banner@example.test>": true},
		},
		{
			name:       "percent-encoded reference",
			html:       `<img src="cid:hero%40example.test">`,
			cids:       map[string]render.InlinePart{"<hero@example.test>": part(3)},
			contentIDs: map[string]string{"<hero@example.test>": "hero@example.test"},
			want:       map[string]bool{"<hero@example.test>": true},
		},
		{
			name:       "srcset, css url and style attribute",
			html:       `<style>.a{background:url('cid:css@x')}</style><img srcset="cid:set@x 1x, https://cdn.test/a.png 2x"><td style="background-image:url(cid:STYLE@x)"></td>`,
			cids:       map[string]render.InlinePart{"css@x": part(4), "set@x": part(5), "style@x": part(6)},
			contentIDs: map[string]string{"css@x": "css@x", "set@x": "set@x", "style@x": "style@x"},
			want:       map[string]bool{"css@x": true, "set@x": true, "style@x": true},
		},
		{
			name:       "inline part with a Content-ID nothing references",
			html:       `<p>No images here, only <a href="cid:link@x">a link</a>.</p>`,
			cids:       map[string]render.InlinePart{"unused@x": part(7), "link@x": part(8)},
			contentIDs: map[string]string{"unused@x": "unused@x", "link@x": "link@x"},
			want:       map[string]bool{"unused@x": false, "link@x": false},
		},
		{
			name:       "src in contexts the render CSP blocks is not referenced",
			html:       `<iframe src="cid:frame@x"></iframe><video src="cid:video@x"></video><audio><source src="cid:audio@x"></audio><embed src="cid:embed@x"><script src="cid:script@x"></script>`,
			cids:       map[string]render.InlinePart{"frame@x": part(11), "video@x": part(12), "audio@x": part(13), "embed@x": part(14), "script@x": part(15)},
			contentIDs: map[string]string{"frame@x": "frame@x", "video@x": "video@x", "audio@x": "audio@x", "embed@x": "embed@x", "script@x": "script@x"},
			want:       map[string]bool{"frame@x": false, "video@x": false, "audio@x": false, "embed@x": false, "script@x": false},
		},
		{
			name:       "other image contexts are referenced",
			html:       `<input type="image" src="cid:button@x"><input type="text" src="cid:text@x"><video poster="cid:poster@x"></video><table background="cid:bg@x"></table><svg><image href="cid:svg@x"/></svg><style>@font-face{font-family:F;src:url(cid:font@x)}</style>`,
			cids:       map[string]render.InlinePart{"button@x": part(16), "text@x": part(17), "poster@x": part(18), "bg@x": part(19), "svg@x": part(20), "font@x": part(21)},
			contentIDs: map[string]string{"button@x": "button@x", "text@x": "text@x", "poster@x": "poster@x", "bg@x": "bg@x", "svg@x": "svg@x", "font@x": "font@x"},
			want:       map[string]bool{"button@x": true, "text@x": false, "poster@x": true, "bg@x": true, "svg@x": true, "font@x": true},
		},
		{
			name:           "part over the inline budget is not referenced",
			html:           `<img src="cid:first@x"><img src="cid:second@x">`,
			cids:           map[string]render.InlinePart{"first@x": part(9), "second@x": part(10)},
			maxInlineBytes: len("data:image/png;base64,") + base64.StdEncoding.EncodedLen(len(part(9).Content)),
			contentIDs:     map[string]string{"first@x": "first@x", "second@x": "second@x"},
			want:           map[string]bool{"first@x": true, "second@x": false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			limit := tc.maxInlineBytes
			if limit == 0 {
				limit = render.MaxInlineBytes
			}
			res := render.RewriteHTML(tc.html, tc.cids, limit)
			for key, p := range tc.cids {
				inlined := bytes.Contains(res.HTML, []byte(base64.StdEncoding.EncodeToString(p.Content)))
				got := res.ReferencesCID(tc.contentIDs[key])
				if got != inlined {
					t.Errorf("%s: ReferencesCID = %v, but rewritten HTML inlined it = %v", key, got, inlined)
				}
				if got != tc.want[key] {
					t.Errorf("%s: ReferencesCID = %v, want %v", key, got, tc.want[key])
				}
			}
		})
	}

	if (render.Result{}).ReferencesCID("anything@x") {
		t.Fatalf("zero Result must not reference any CID")
	}
	if (render.Result{}).ReferencesCID("") {
		t.Fatalf("empty Content-ID must never be referenced")
	}
}
