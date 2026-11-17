package syncer

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/shi-gg/facebook-to-bluesky/internal/bluesky"
	"github.com/shi-gg/facebook-to-bluesky/internal/config"
	"github.com/shi-gg/facebook-to-bluesky/internal/feed"
)

type Status struct {
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	Interval    string     `json:"interval"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Published   int        `json:"published"`
	Skipped     int        `json:"skipped"`
	LastPost    string     `json:"last_post,omitempty"`
}

type Syncer struct {
	feed    config.Feed
	fetcher *feed.Fetcher
	log     *slog.Logger

	mu     sync.Mutex
	client *bluesky.Client
	status Status
}

func New(cfg config.Feed, logger *slog.Logger) *Syncer {
	return &Syncer{
		feed:    cfg,
		fetcher: feed.NewFetcher(),
		log:     logger.With("feed", cfg.Name),
		status: Status{
			Name:     cfg.Name,
			URL:      cfg.URL,
			Interval: cfg.Interval.String(),
		},
	}
}

func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Syncer) Run(ctx context.Context) {
	s.log.Info("starting feed", "interval", s.feed.Interval.String(), "max_age", s.feed.MaxAge.String())

	s.run(ctx)

	ticker := time.NewTicker(s.feed.Interval.Duration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.run(ctx)
		}
	}
}

func (s *Syncer) run(ctx context.Context) {
	started := time.Now()
	s.updateStatus(func(status *Status) {
		status.LastRun = &started
		status.Published = 0
		status.Skipped = 0
	})

	hadError := false

	client, err := s.clientFor(ctx)
	if err != nil {
		hadError = true
		s.fail(err)
		return
	}

	posts, err := s.fetcher.Fetch(ctx, s.feed.URL)
	if err != nil {
		hadError = true
		s.fail(err)
		return
	}

	sort.SliceStable(posts, func(i, j int) bool {
		return posts[i].Published.Before(posts[j].Published)
	})

	published, skipped := 0, 0

	for _, post := range posts {
		if ctx.Err() != nil {
			return
		}
		if s.feed.MaxPostsPerRun > 0 && published >= s.feed.MaxPostsPerRun {
			s.log.Debug("per-run post limit reached", "limit", s.feed.MaxPostsPerRun)
			break
		}
		if post.ID == "" || s.tooOld(post) {
			skipped++
			continue
		}

		exists, err := client.Exists(ctx, post.ID)
		if err != nil {
			hadError = true
			s.fail(err)
			continue
		}
		if exists {
			skipped++
			continue
		}

		text := composeText(post, s.feed.LinkEnabled())
		if text == "" && len(post.Images) == 0 {
			skipped++
			continue
		}

		createdAt := post.Published
		if createdAt.IsZero() || createdAt.After(time.Now().Add(time.Minute)) {
			createdAt = time.Now()
		}

		uri, _, err := client.Publish(ctx, bluesky.Post{
			Text:       text,
			Images:     post.Images,
			CreatedAt:  createdAt,
			FacebookID: post.ID,
		})
		if err != nil {
			hadError = true
			s.fail(err)
			continue
		}

		published++
		s.log.Info("published post", "id", post.ID, "uri", uri)
		s.updateStatus(func(status *Status) {
			status.LastPost = uri
		})
	}

	s.updateStatus(func(status *Status) {
		status.Published = published
		status.Skipped = skipped
		if !hadError {
			success := time.Now()
			status.LastSuccess = &success
			status.LastError = ""
		}
	})

	s.log.Info("poll finished", "published", published, "skipped", skipped)
}

func (s *Syncer) clientFor(ctx context.Context) (*bluesky.Client, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()

	if client != nil {
		return client, nil
	}

	client, err := bluesky.Login(ctx, s.feed.Bluesky.Handle, s.feed.Bluesky.Password, s.feed.Bluesky.PDS, s.log)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.client = client
	s.mu.Unlock()

	s.log.Info("logged in", "did", client.DID())
	return client, nil
}

func (s *Syncer) tooOld(post feed.Post) bool {
	maxAge := s.feed.MaxAge.Duration()
	if maxAge < 0 || post.Published.IsZero() {
		return false
	}
	return time.Since(post.Published) > maxAge
}

func (s *Syncer) fail(err error) {
	s.log.Error("poll failed", "error", err)
	s.updateStatus(func(status *Status) {
		status.LastError = err.Error()
	})
}

func (s *Syncer) updateStatus(update func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.status)
}
