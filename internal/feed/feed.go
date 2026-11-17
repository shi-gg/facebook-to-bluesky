package feed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/extensions"
)

const (
	maxFeedBytes = 10 << 20
	maxImages    = 4
	fetchTimeout = 30 * time.Second
	defaultUA    = "facebook-to-bluesky (+https://github.com/shi-gg/facebook-to-bluesky)"
)

type Post struct {
	ID        string
	URL       string
	Title     string
	Text      string
	Images    []string
	Published time.Time
}

type Fetcher struct {
	client    *http.Client
	userAgent string
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		client:    &http.Client{Timeout: fetchTimeout},
		userAgent: defaultUA,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, feedURL string) ([]Post, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build feed request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch feed: HTTP %d", resp.StatusCode)
	}

	parsed, err := gofeed.NewParser().Parse(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	posts := make([]Post, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		if item == nil {
			continue
		}
		posts = append(posts, convert(item, feedURL))
	}

	return posts, nil
}

func convert(item *gofeed.Item, feedURL string) Post {
	text, htmlImages := cleanHTML(item.Description)
	if text == "" {
		text, _ = cleanHTML(item.Title)
	}

	published := time.Time{}
	if item.PublishedParsed != nil {
		published = item.PublishedParsed.UTC()
	}

	return Post{
		ID:        itemID(item, feedURL),
		URL:       strings.TrimSpace(item.Link),
		Title:     strings.TrimSpace(item.Title),
		Text:      text,
		Images:    collectImages(item, htmlImages),
		Published: published,
	}
}

func itemID(item *gofeed.Item, feedURL string) string {
	if id := strings.TrimSpace(item.GUID); id != "" {
		return id
	}
	if link := strings.TrimSpace(item.Link); link != "" {
		return link
	}

	sum := sha256.Sum256([]byte(feedURL + "\x00" + item.Title + "\x00" + item.Description))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func collectImages(item *gofeed.Item, htmlImages []string) []string {
	base, _ := url.Parse(item.Link)

	candidates := make([]string, 0, 4+len(htmlImages))
	if item.Image != nil {
		candidates = append(candidates, item.Image.URL)
	}
	for _, enclosure := range item.Enclosures {
		if enclosure == nil {
			continue
		}
		if enclosure.Type == "" || strings.HasPrefix(enclosure.Type, "image/") {
			candidates = append(candidates, enclosure.URL)
		}
	}
	candidates = append(candidates, htmlImages...)
	candidates = append(candidates, mediaImages(item)...)

	seen := make(map[string]struct{}, len(candidates))
	images := make([]string, 0, maxImages)
	for _, raw := range candidates {
		imageURL := normalizeURL(base, raw)
		if imageURL == "" {
			continue
		}
		if _, ok := seen[imageURL]; ok {
			continue
		}

		seen[imageURL] = struct{}{}
		images = append(images, imageURL)
		if len(images) == maxImages {
			break
		}
	}

	return images
}

func mediaImages(item *gofeed.Item) []string {
	var images []string

	appendExtension := func(extension map[string][]ext.Extension) {
		for _, name := range []string{"content", "thumbnail"} {
			for _, item := range extension[name] {
				if raw := item.Attrs["url"]; raw != "" {
					images = append(images, raw)
				}
			}
		}
	}

	appendExtension(item.Extensions["media"])
	for _, group := range item.Extensions["media"]["group"] {
		appendExtension(group.Children)
	}

	return images
}

func normalizeURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "data:") {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}

	return parsed.String()
}
