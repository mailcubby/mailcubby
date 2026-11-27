package compat

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/jhillyerd/enmime/v2"
)

// DefaultCacheSize is the default LRU entry capacity for compatibility reports.
const DefaultCacheSize = 64

// ErrNoHTML is returned when a message has no HTML part to analyze.
var ErrNoHTML = errors.New("compat: message has no HTML part")

// BlobOpener opens a decompressed message blob by its relative path.
type BlobOpener interface {
	Open(relPath string) (io.ReadCloser, error)
}

type cacheKey struct {
	projectID int64
	msgID     string
}

type cacheEntry struct {
	key    cacheKey
	report Report
}

// Checker analyzes email HTML/CSS against the embedded caniemail dataset and
// caches recent reports in a bounded LRU keyed by {projectID, msgID}.
type Checker struct {
	blobs     BlobOpener
	dataset   *Dataset
	cacheSize int

	mu    sync.Mutex
	order *list.List
	items map[cacheKey]*list.Element
}

// New creates a Checker backed by blobs with DefaultCacheSize (64) LRU capacity.
func New(blobs BlobOpener) (*Checker, error) {
	return NewWithCacheSize(blobs, DefaultCacheSize)
}

// NewWithCacheSize creates a Checker with a custom LRU capacity (0 disables caching).
func NewWithCacheSize(blobs BlobOpener, cacheSize int) (*Checker, error) {
	ds, err := LoadDataset()
	if err != nil {
		return nil, err
	}
	if cacheSize < 0 {
		cacheSize = 0
	}
	return &Checker{
		blobs:     blobs,
		dataset:   ds,
		cacheSize: cacheSize,
		order:     list.New(),
		items:     make(map[cacheKey]*list.Element, cacheSize),
	}, nil
}

// Dataset returns the immutable embedded caniemail dataset used by this Checker.
func (c *Checker) Dataset() *Dataset {
	return c.dataset
}

// Check loads a message blob, extracts its HTML part (and inline CID content types),
// runs compatibility analysis, and caches the resulting Report.
// The returned Report slices must be treated as read-only by callers.
func (c *Checker) Check(ctx context.Context, projectID int64, msgID, blobPath string) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	key := cacheKey{projectID: projectID, msgID: msgID}
	if cached, ok := c.getCached(key); ok {
		return cached, nil
	}

	if c.blobs == nil {
		return Report{}, errors.New("compat: blob opener is not configured")
	}
	rc, err := c.blobs.Open(blobPath)
	if err != nil {
		return Report{}, fmt.Errorf("compat: open blob %s: %w", msgID, err)
	}
	env, parseErr := enmime.NewParser().ReadEnvelope(rc)
	_ = rc.Close()
	if parseErr != nil {
		return Report{}, fmt.Errorf("compat: parse message %s: %w", msgID, parseErr)
	}
	if strings.TrimSpace(env.HTML) == "" {
		return Report{}, ErrNoHTML
	}

	cidTypes := collectCIDContentTypes(env)
	report := c.AnalyzeHTMLWithCIDs(env.HTML, cidTypes)
	c.putCached(key, report)
	return report, nil
}

// AnalyzeHTML runs offline compatibility analysis directly on an HTML string.
func (c *Checker) AnalyzeHTML(rawHTML string) Report {
	return c.AnalyzeHTMLWithCIDs(rawHTML, nil)
}

// AnalyzeHTMLWithCIDs runs offline compatibility analysis on rawHTML with an
// optional map of normalized Content-ID -> MIME content type for cid: images.
func (c *Checker) AnalyzeHTMLWithCIDs(rawHTML string, cidContentTypes map[string]string) Report {
	res := analyzeDocument(rawHTML, cidContentTypes)
	return buildReport(c.dataset, res)
}

func (c *Checker) getCached(key cacheKey) (Report, bool) {
	if c.cacheSize == 0 {
		return Report{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return Report{}, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(cacheEntry).report, true
}

func (c *Checker) putCached(key cacheKey, report Report) {
	if c.cacheSize == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		elem.Value = cacheEntry{key: key, report: report}
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(cacheEntry{key: key, report: report})
	c.items[key] = elem
	for c.order.Len() > c.cacheSize {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(cacheEntry)
		delete(c.items, entry.key)
		c.order.Remove(oldest)
	}
}

func collectCIDContentTypes(env *enmime.Envelope) map[string]string {
	cids := make(map[string]string)
	addParts := func(parts []*enmime.Part) {
		for _, p := range parts {
			if p == nil {
				continue
			}
			cid := normalizeCID(p.ContentID)
			if cid == "" {
				continue
			}
			if _, exists := cids[cid]; exists {
				continue
			}
			cids[cid] = p.ContentType
		}
	}
	addParts(env.Inlines)
	addParts(env.Attachments)
	addParts(env.OtherParts)
	return cids
}
