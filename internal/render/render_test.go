package render_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mailcubby/mailcubby/internal/render"
)

func TestRewriteHTMLSpec121Rules(t *testing.T) {
	t.Parallel()

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	wantDataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	cases := []struct {
		name            string
		input           string
		cids            map[string]render.InlinePart
		maxInlineBytes  int
		wantContain     []string
		wantNotContain  []string
		wantRemoteCount int
	}{
		{
			name: "injects head tags, drops base/refresh/prefetch, keeps script, inlines cid, counts remotes",
			input: `<!doctype html><html><head>` +
				`<base href="https://evil.test/">` +
				`<meta http-equiv="refresh" content="0;url=https://evil.test/">` +
				`<link rel="dns-prefetch" href="https://tracker.test">` +
				`<link rel="Preconnect stylesheet" href="https://fonts.test">` +
				`<link rel="preload" as="image" href="https://tracker.test/p.png">` +
				`<link rel="prefetch" href="https://tracker.test/next">` +
				`<link rel="prerender" href="https://tracker.test/pre">` +
				`<title>Test</title>` +
				`<style>.hero { background-image: url(cid:logo@example.test); } .ext { background: url("https://cdn.test/bg.png"); } /* url(https://comment.test/ignore.png) */</style>` +
				`</head><body>` +
				`<script>window.__pwned = true;</script>` +
				`<img src="cid:logo@example.test">` +
				`<img src="https://cdn.test/a.png" srcset="https://cdn.test/a-1x.png 1x, https://cdn.test/a-2x.png 2x">` +
				`<td background="http://cdn.test/cell.gif" style="background: url('https://cdn.test/inline.jpg')"></td>` +
				`<a href="https://mailcubby.dev">Normal link is not a remote resource</a>` +
				`</body></html>`,
			cids: map[string]render.InlinePart{
				"<logo@example.test>": {ContentType: "image/png; name=\"logo.png\"", Content: pngBytes},
			},
			maxInlineBytes: render.MaxInlineBytes,
			wantContain: []string{
				`<head><meta name="referrer" content="no-referrer"><base target="_blank">`,
				`<title>Test</title>`,
				`<script>window.__pwned = true;</script>`,
				`src="` + wantDataURI + `"`,
				`url("` + wantDataURI + `")`,
				`<a href="https://mailcubby.dev">`,
			},
			wantNotContain: []string{
				`https://evil.test/`,
				`http-equiv="refresh"`,
				`dns-prefetch`,
				`Preconnect`,
				`rel="preload"`,
				`rel="prefetch"`,
				`rel="prerender"`,
				`cid:logo@example.test`,
			},
			// 1 CSS style block + 1 img src + 2 srcset + 1 td background + 1 inline style = 6
			wantRemoteCount: 6,
		},
		{
			name:  "creates head when html lacks head element",
			input: `<!doctype html><html lang="en"><body><p>Hello</p></body></html>`,
			wantContain: []string{
				`<!doctype html><html lang="en"><head><meta name="referrer" content="no-referrer"><base target="_blank"></head><body><p>Hello</p></body></html>`,
			},
			wantRemoteCount: 0,
		},
		{
			name:  "creates head for bare html fragment",
			input: `<p>Fragment only</p>`,
			wantContain: []string{
				`<head><meta name="referrer" content="no-referrer"><base target="_blank"></head><p>Fragment only</p>`,
			},
			wantRemoteCount: 0,
		},
		{
			name:  "respects max inline bytes budget and leaves overflow cid broken",
			input: `<html><head></head><body><img src="cid:a@test"><img src="cid:b@test"></body></html>`,
			cids: map[string]render.InlinePart{
				"a@test": {ContentType: "image/png", Content: pngBytes},
				"b@test": {ContentType: "image/png", Content: make([]byte, 200)},
			},
			maxInlineBytes: len(wantDataURI) + 10,
			wantContain: []string{
				`src="` + wantDataURI + `"`,
				`src="cid:b@test"`,
			},
			wantRemoteCount: 0,
		},
		{
			name:  "handles style block with > selector without html escaping CSS",
			input: `<html><head><style>div > p { color: red; }</style></head><body><div><p>Hi</p></div></body></html>`,
			wantContain: []string{
				`<style>div > p { color: red; }</style>`,
			},
			wantNotContain: []string{
				`div &gt; p`,
			},
			wantRemoteCount: 0,
		},
		{
			name:            "counts protocol-relative and @import remote urls",
			input:           `<style>@import "https://cdn.test/a.css";</style><img src="//cdn.test/pixel.png">`,
			wantRemoteCount: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := render.RewriteHTML(tc.input, tc.cids, tc.maxInlineBytes)
			got := string(res.HTML)
			for _, sub := range tc.wantContain {
				if !strings.Contains(got, sub) {
					t.Errorf("rewritten HTML missing %q\ngot: %s", sub, got)
				}
			}
			for _, sub := range tc.wantNotContain {
				if strings.Contains(got, sub) {
					t.Errorf("rewritten HTML unexpectedly contains %q\ngot: %s", sub, got)
				}
			}
			if res.RemoteResourceCount != tc.wantRemoteCount {
				t.Errorf("RemoteResourceCount = %d, want %d", res.RemoteResourceCount, tc.wantRemoteCount)
			}
		})
	}
}

type fakeBlobOpener struct {
	blobs map[string][]byte
	opens atomic.Int64
}

func (f *fakeBlobOpener) Open(relPath string) (io.ReadCloser, error) {
	f.opens.Add(1)
	b, ok := f.blobs[relPath]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func TestRendererCacheAndNoHTML(t *testing.T) {
	t.Parallel()

	htmlEML := "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: HTML\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Hi <img src=\"https://cdn.test/x.png\"></p>"

	textEML := "From: a@example.test\r\n" +
		"To: b@example.test\r\n" +
		"Subject: Plain\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Just plain text"

	opener := &fakeBlobOpener{
		blobs: map[string][]byte{
			"p1/m1": []byte(htmlEML),
			"p1/m2": []byte(htmlEML),
			"p1/m3": []byte(htmlEML),
			"p1/pt": []byte(textEML),
		},
	}

	r := render.NewWithCacheSize(opener, 2)
	ctx := context.Background()

	// Text-only message returns ErrNoHTML.
	if _, err := r.Render(ctx, 1, "pt", "p1/pt"); !errors.Is(err, render.ErrNoHTML) {
		t.Fatalf("text-only Render err = %v, want ErrNoHTML", err)
	}

	// First render opens blob; second render hits LRU cache.
	res1, err := r.Render(ctx, 1, "m1", "p1/m1")
	if err != nil {
		t.Fatalf("Render m1: %v", err)
	}
	if res1.RemoteResourceCount != 1 {
		t.Fatalf("RemoteResourceCount = %d, want 1", res1.RemoteResourceCount)
	}
	opensAfterFirst := opener.opens.Load()

	res2, err := r.Render(ctx, 1, "m1", "p1/m1")
	if err != nil {
		t.Fatalf("Render m1 cached: %v", err)
	}
	if !bytes.Equal(res1.HTML, res2.HTML) {
		t.Fatalf("cached render HTML differs")
	}
	if opener.opens.Load() != opensAfterFirst {
		t.Fatalf("expected cache hit without reopening blob")
	}

	// Fill cache beyond capacity 2 -> evicts oldest (m2 after touching m1).
	if _, err := r.Render(ctx, 1, "m2", "p1/m2"); err != nil {
		t.Fatalf("Render m2: %v", err)
	}
	if _, err := r.Render(ctx, 1, "m1", "p1/m1"); err != nil {
		t.Fatalf("Render m1 touch: %v", err)
	}
	if _, err := r.Render(ctx, 1, "m3", "p1/m3"); err != nil {
		t.Fatalf("Render m3: %v", err)
	}
	opensBeforeEvicted := opener.opens.Load()
	if _, err := r.Render(ctx, 1, "m2", "p1/m2"); err != nil {
		t.Fatalf("Render m2 after eviction: %v", err)
	}
	if opener.opens.Load() != opensBeforeEvicted+1 {
		t.Fatalf("expected m2 to be evicted and reopened")
	}
}
