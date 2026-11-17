package bluesky

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

const (
	postCollection = "app.bsky.feed.post"
	userAgent      = "facebook-to-bluesky (+https://github.com/shi-gg/facebook-to-bluesky)"
)

type Client struct {
	api  *atclient.APIClient
	did  string
	http *http.Client
	log  *slog.Logger
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
		api:  api,
		did:  api.AccountDID.String(),
		http: api.Client,
		log:  logger,
	}, nil
}

func (c *Client) DID() string {
	return c.did
}

func RecordKey(sourceID string) string {
	sum := sha256.Sum256([]byte(sourceID))
	return hex.EncodeToString(sum[:])
}

func (c *Client) Exists(ctx context.Context, sourceID string) (bool, error) {
	_, err := atproto.RepoGetRecord(ctx, c.api, "", postCollection, c.did, RecordKey(sourceID))
	if err == nil {
		return true, nil
	}

	var apiErr *atclient.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusNotFound || apiErr.Name == "RecordNotFound" {
			return false, nil
		}
	}

	return false, fmt.Errorf("look up %s: %w", RecordKey(sourceID), err)
}

func normalizePDS(pds string) string {
	pds = strings.TrimRight(strings.TrimSpace(pds), "/")
	if pds != "" && !strings.Contains(pds, "://") {
		pds = "https://" + pds
	}
	return pds
}
