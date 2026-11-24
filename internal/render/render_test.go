package render_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
		{
			name: "strips meta refresh and prefetch inside noscript and counts remote images",
			input: `<html><head><noscript><meta http-equiv="refresh" content="0;url=https://evil.test/ns"></noscript></head>` +
				`<body><noscript><base href="https://evil.test/"><link rel="prefetch" href="https://evil.test/p"><img src="https://evil.test/px.png"><p>ok</p></noscript></body></html>`,
			wantContain: []string{
				`<noscript></noscript>`,
				`<p>ok</p>`,
			},
			wantNotContain: []string{
				`http-equiv="refresh"`,
				`https://evil.test/ns`,
				`https://evil.test/p"`,
				`<base href="https://evil.test/">`,
			},
			wantRemoteCount: 1,
		},
		{
			name: "strips meta refresh and preload inside svg style and svg title foreign rawtext",
			input: `<html><body><svg><style><meta http-equiv="refresh" content="0;url=https://evil.test/svg">` +
				`<link rel="preload" as="image" href="https://evil.test/pre.png">` +
				`.a { background: url("https://evil.test/bg.png"); }</style>` +
				`<title><meta http-equiv="refresh" content="0;url=https://evil.test/title"></title>` +
				`<image href="https://evil.test/img.png"/></svg></body></html>`,
			wantContain: []string{
				`<svg><style>`,
				`url("https://evil.test/bg.png")`,
			},
			wantNotContain: []string{
				`http-equiv="refresh"`,
				`https://evil.test/svg`,
				`https://evil.test/pre.png`,
				`https://evil.test/title`,
			},
			wantRemoteCount: 2,
		},
		{
			name:  "strips meta refresh inside iframe srcdoc and counts remote images",
			input: `<html><body><iframe srcdoc="<script>window.__pwned=1</script><meta http-equiv='refresh' content='0;url=https://evil.test/sd'><img src='https://evil.test/sd.png'>"></iframe></body></html>`,
			wantContain: []string{
				`<iframe srcdoc=`,
				`window.__pwned=1`,
			},
			wantNotContain: []string{
				`evil.test/sd'`,
				`http-equiv`,
			},
			wantRemoteCount: 1,
		},
		{
			name:            "drops modulepreload link",
			input:           `<head><link rel="modulepreload" href="https://evil.test/m.js"></head><body>ok</body>`,
			wantNotContain:  []string{`modulepreload`, `evil.test/m.js`},
			wantRemoteCount: 0,
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

func TestHostileCorpusRewrite(t *testing.T) {
	t.Parallel()

	hostileFS := os.DirFS(filepath.Join("..", "..", "testdata", "hostile"))
	matches, err := fs.Glob(hostileFS, "*.eml")
	if err != nil {
		t.Fatalf("glob hostile fixtures: %v", err)
	}
	if len(matches) < 12 {
		t.Fatalf("expected at least 12 hostile fixtures, found %d", len(matches))
	}

	wantRemote := map[string]bool{
		"03-noscript-meta-refresh.eml":  true,
		"04-svg-style-meta-refresh.eml": true,
		"08-svg-script.eml":             true,
		"09-css-url-exfil.eml":          true,
		"11-iframe-object.eml":          true,
		"12-srcset-tracking.eml":        true,
	}

	for _, name := range matches {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := fs.ReadFile(hostileFS, name)
			if err != nil {
				t.Fatalf("read fixture %s: %v", name, err)
			}
			opener := &fakeBlobOpener{blobs: map[string][]byte{name: raw}}
			r := render.New(opener)
			res, err := r.Render(context.Background(), 1, name, name)
			if err != nil {
				t.Fatalf("Render(%s): %v", name, err)
			}
			got := string(res.HTML)
			lower := strings.ToLower(got)

			if !strings.Contains(got, `<meta name="referrer" content="no-referrer"><base target="_blank">`) {
				t.Errorf("%s: missing injected referrer/base tags", name)
			}
			for _, forbidden := range []string{
				`http-equiv="refresh"`,
				`http-equiv='refresh'`,
				`http-equiv=refresh`,
				`<base href=`,
				`rel="dns-prefetch"`,
				`rel="preconnect"`,
				`rel="prefetch"`,
				`rel="preload"`,
				`rel="prerender"`,
				`rel="modulepreload"`,
			} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("%s: rewritten HTML still contains forbidden %q\nhtml: %s", name, forbidden, got)
				}
			}
			if wantRemote[name] && res.RemoteResourceCount <= 0 {
				t.Errorf("%s: expected RemoteResourceCount > 0, got %d", name, res.RemoteResourceCount)
			}
		})
	}
}

func TestRewriteHTMLDeepNoscriptBounded(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for range 25 {
		b.WriteString("<noscript>")
	}
	b.WriteString(`<meta http-equiv="refresh" content="0;url=https://evil.test/deep">`)
	for range 25 {
		b.WriteString("</noscript>")
	}

	res := render.RewriteHTML(b.String(), nil, render.MaxInlineBytes)
	lower := strings.ToLower(string(res.HTML))
	if strings.Contains(lower, `<meta http-equiv="refresh"`) {
		t.Fatalf("deeply nested noscript allowed raw meta refresh through: %s", lower)
	}
}
