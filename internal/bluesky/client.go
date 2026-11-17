package bluesky

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

const (
	postCollection = "app.bsky.feed.post"
	userAgent      = "facebook-to-bluesky (+https://github.com/shi-gg/facebook-to-bluesky)"
	listPageSize   = 100
)

type Client struct {
	api  *atclient.APIClient
	did  string
	http *http.Client
	log  *slog.Logger

	mu          sync.Mutex
	known       map[string]struct{}
	indexLoaded bool
	tid         *syntax.TIDClock
}

func Login(ctx context.Context, handle, password, pds string, logger *slog.Logger) (*Client, error) {
	identifier, err := syntax.ParseAtIdentifier(handle)
	if err != nil {
		return nil, fmt.Errorf("invalid handle or DID %q: %w", handle, err)
	}

	var api *atclient.APIClient
	if pds = normalizePDS(pds); pds != "" {
		api, err = atclient.LoginWithPasswordHost(ctx, pds, identifier.String(), password, "", nil)
	} else {
		api, err = atclient.LoginWithPassword(ctx, identity.DefaultDirectory(), identifier, password, "", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	api.Client = &http.Client{Timeout: 60 * time.Second}
	api.Headers.Set("User-Agent", userAgent)

	if api.AccountDID == nil {
		return nil, fmt.Errorf("login: session did not include a DID")
	}

	return &Client{
		api:   api,
		did:   api.AccountDID.String(),
		http:  api.Client,
		log:   logger,
		known: make(map[string]struct{}),
		tid:   syntax.NewTIDClock(0),
	}, nil
}

func (c *Client) DID() string {
	return c.did
}

func (c *Client) Exists(ctx context.Context, sourceID string) (bool, error) {
	if err := c.loadIndex(ctx); err != nil {
		return false, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.known[sourceID]
	return ok, nil
}

func (c *Client) nextRKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tid.Next().String()
}

func (c *Client) markPublished(sourceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known[sourceID] = struct{}{}
}

type listRecordsOutput struct {
	Cursor  *string `json:"cursor,omitempty"`
	Records []struct {
		Value json.RawMessage `json:"value"`
	} `json:"records"`
}

type indexedRecord struct {
	XFacebookID string `json:"x-facebook-id"`
}

func (c *Client) loadIndex(ctx context.Context) error {
	c.mu.Lock()
	loaded := c.indexLoaded
	c.mu.Unlock()
	if loaded {
		return nil
	}

	known := make(map[string]struct{})
	cursor := ""

	for {
		var out listRecordsOutput
		params := map[string]any{
			"repo":       c.did,
			"collection": postCollection,
			"limit":      int64(listPageSize),
		}
		if cursor != "" {
			params["cursor"] = cursor
		}

		err := c.api.LexDo(ctx, http.MethodGet, "", "com.atproto.repo.listRecords", params, nil, &out)
		if err != nil {
			return fmt.Errorf("list posts: %w", err)
		}

		for _, record := range out.Records {
			var value indexedRecord
			if err := json.Unmarshal(record.Value, &value); err != nil {
				return fmt.Errorf("decode indexed post: %w", err)
			}
			if value.XFacebookID != "" {
				known[value.XFacebookID] = struct{}{}
			}
		}

		if out.Cursor == nil || *out.Cursor == "" || *out.Cursor == cursor {
			break
		}
		cursor = *out.Cursor
	}

	c.mu.Lock()
	for id := range known {
		c.known[id] = struct{}{}
	}
	c.indexLoaded = true
	c.mu.Unlock()

	return nil
}

func normalizePDS(pds string) string {
	pds = strings.TrimRight(strings.TrimSpace(pds), "/")
	if pds != "" && !strings.Contains(pds, "://") {
		pds = "https://" + pds
	}
	return pds
}
