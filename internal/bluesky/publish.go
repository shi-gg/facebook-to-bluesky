package bluesky

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	_ "golang.org/x/image/webp"
)

const maxImageBytes = 2_000_000

type Post struct {
	Text       string
	Images     []string
	CreatedAt  time.Time
	FacebookID string
}

type postRecord struct {
	Type        string                `json:"$type"`
	Text        string                `json:"text"`
	CreatedAt   string                `json:"createdAt"`
	Facets      []*bsky.RichtextFacet `json:"facets,omitempty"`
	Embed       *bsky.FeedPost_Embed  `json:"embed,omitempty"`
	XFacebookID string                `json:"x-facebook-id,omitempty"`
}

type createRecordInput struct {
	Repo       string `json:"repo"`
	Collection string `json:"collection"`
	Rkey       string `json:"rkey"`
	Record     any    `json:"record"`
	Validate   *bool  `json:"validate,omitempty"`
}

type createRecordOutput struct {
	Uri string `json:"uri"`
	Cid string `json:"cid"`
}

func buildRecord(post Post) *postRecord {
	return &postRecord{
		Type:        postCollection,
		Text:        post.Text,
		CreatedAt:   post.CreatedAt.UTC().Format(time.RFC3339Nano),
		Facets:      buildFacets(post.Text),
		XFacebookID: post.FacebookID,
	}
}

func (c *Client) Publish(ctx context.Context, post Post) (uri, cid string, err error) {
	if c == nil || c.api == nil {
		return "", "", fmt.Errorf("authenticated client required")
	}
	if len(post.Images) > 4 {
		return "", "", fmt.Errorf("maximum 4 images allowed")
	}
	if strings.TrimSpace(post.Text) == "" && len(post.Images) == 0 {
		return "", "", fmt.Errorf("post cannot be empty")
	}
	if strings.TrimSpace(post.FacebookID) == "" {
		return "", "", fmt.Errorf("facebook id required")
	}
	if post.CreatedAt.IsZero() {
		post.CreatedAt = time.Now()
	}

	record := buildRecord(post)

	images := c.uploadImages(ctx, post.Images)
	if len(images) > 0 {
		record.Embed = &bsky.FeedPost_Embed{
			EmbedImages: &bsky.EmbedImages{
				LexiconTypeID: "app.bsky.embed.images",
				Images:        images,
			},
		}
	}

	var out createRecordOutput
	err = c.api.LexDo(
		ctx,
		http.MethodPost,
		"application/json",
		"com.atproto.repo.createRecord",
		nil,
		createRecordInput{
			Repo:       c.did,
			Collection: postCollection,
			Rkey:       RecordKey(post.FacebookID),
			Record:     record,
		},
		&out,
	)
	if err != nil {
		return "", "", fmt.Errorf("publish record: %w", err)
	}

	return out.Uri, out.Cid, nil
}

func (c *Client) uploadImages(ctx context.Context, imageURLs []string) []*bsky.EmbedImages_Image {
	images := make([]*bsky.EmbedImages_Image, 0, len(imageURLs))

	for _, imageURL := range imageURLs {
		data, mime, ratio, err := downloadImage(ctx, c.http, imageURL)
		if err != nil {
			c.log.Warn("skipping image", "url", imageURL, "error", err)
			continue
		}

		var uploaded atproto.RepoUploadBlob_Output
		err = c.api.LexDo(
			ctx,
			http.MethodPost,
			mime,
			"com.atproto.repo.uploadBlob",
			nil,
			bytes.NewReader(data),
			&uploaded,
		)
		if err != nil {
			c.log.Warn("skipping image upload", "url", imageURL, "error", err)
			continue
		}
		if uploaded.Blob == nil {
			c.log.Warn("skipping image upload", "url", imageURL, "error", "missing blob")
			continue
		}

		images = append(images, &bsky.EmbedImages_Image{
			Alt:         "",
			Image:       uploaded.Blob,
			AspectRatio: ratio,
		})
	}

	return images
}

func downloadImage(ctx context.Context, client *http.Client, rawURL string) ([]byte, string, *bsky.EmbedDefs_AspectRatio, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, "", nil, fmt.Errorf("invalid HTTP(S) image URL")
	}

	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", nil, err
	}
	if len(data) > maxImageBytes {
		return nil, "", nil, fmt.Errorf("image exceeds 2 MB")
	}

	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		return nil, "", nil, fmt.Errorf("unsupported image: %s", mime)
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", nil, fmt.Errorf("decode image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", nil, fmt.Errorf("invalid image dimensions")
	}

	return data, mime, &bsky.EmbedDefs_AspectRatio{
		Width:  int64(cfg.Width),
		Height: int64(cfg.Height),
	}, nil
}
