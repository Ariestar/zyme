package ingest

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// RSS adapter: parse an RSS/Atom/JSON feed and emit one payload per item.
// Covers blogs, news, YouTube, Reddit, and — via RSSHub — 公众号/微博/知乎/B站/etc.
type RSS struct{}

func (RSS) ID() string { return "rss" }

func (RSS) Fetch(ctx context.Context, ref SourceRef) ([]IngestPayload, error) {
	feed, err := gofeed.NewParser().ParseURL(ref.URI)
	if err != nil {
		return nil, fmt.Errorf("parse feed %s: %w", ref.URI, err)
	}
	out := make([]IngestPayload, 0, len(feed.Items))
	for _, item := range feed.Items {
		html := item.Content
		if html == "" {
			html = item.Description
		}
		guid := item.GUID
		if guid == "" {
			guid = item.Link
		}
		if guid == "" {
			continue // no stable identity; skip
		}
		out = append(out, IngestPayload{
			Identity:      contentHash(guid),
			IdentityBasis: "guid",
			Kind:          "feed_item",
			SourceURI:     firstNonEmpty(item.Link, ref.URI),
			Title:         item.Title,
			Markdown:      stripTags(html),
			Snapshot:      []byte(html),
			SnapshotMIME:  "text/html",
			FetchedAt:     firstTime(item.PublishedParsed, item.UpdatedParsed),
			AdapterID:     "rss",
		})
	}
	return out, nil
}

var (
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	styleRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	wsRe     = regexp.MustCompile(`[ \t\r\n\f]+`)
)

// stripTags is a lossy HTML->text conversion for embedding input.
// TODO: replace with bluemonday if feed embedding quality matters.
func stripTags(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = styleRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, "")
	s = wsRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstTime(ts ...*time.Time) time.Time {
	for _, t := range ts {
		if t != nil {
			return *t
		}
	}
	return time.Now()
}
