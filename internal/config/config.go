package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}

	*d = Duration(parsed)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

const (
	DefaultLogLevel       = "info"
	DefaultPort           = 8080
	DefaultInterval       = 15 * time.Minute
	DefaultMaxAge         = 24 * time.Hour
	DefaultMaxPostsPerRun = 5
)

type Config struct {
	LogLevel string `yaml:"log_level"`
	Port     int    `yaml:"port"`
	Feeds    []Feed `yaml:"feeds"`
}

type Feed struct {
	Name           string   `yaml:"name"`
	URL            string   `yaml:"url"`
	Interval       Duration `yaml:"interval"`
	MaxAge         Duration `yaml:"max_age"`
	MaxPostsPerRun int      `yaml:"max_posts_per_run"`
	IncludeLink    *bool    `yaml:"include_link"`
	Bluesky        Bluesky  `yaml:"bluesky"`
}

type Bluesky struct {
	Handle   string `yaml:"handle"`
	Password string `yaml:"password"`
	PDS      string `yaml:"pds"`
}

func (f Feed) LinkEnabled() bool {
	return f.IncludeLink == nil || *f.IncludeLink
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(raw))))
	decoder.KnownFields(true)

	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
	if c.LogLevel == "" {
		c.LogLevel = DefaultLogLevel
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}

	for i := range c.Feeds {
		feed := &c.Feeds[i]
		feed.URL = strings.TrimSpace(feed.URL)
		feed.Name = strings.TrimSpace(feed.Name)
		if feed.Name == "" {
			feed.Name = feed.URL
		}
		if feed.Interval == 0 {
			feed.Interval = Duration(DefaultInterval)
		}
		if feed.MaxAge == 0 {
			feed.MaxAge = Duration(DefaultMaxAge)
		}
		if feed.MaxPostsPerRun == 0 {
			feed.MaxPostsPerRun = DefaultMaxPostsPerRun
		}
		feed.Bluesky.Handle = strings.TrimSpace(feed.Bluesky.Handle)
		feed.Bluesky.Password = strings.TrimSpace(feed.Bluesky.Password)
		feed.Bluesky.PDS = strings.TrimRight(strings.TrimSpace(feed.Bluesky.PDS), "/")
	}
}

func (c *Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level must be one of debug, info, warn, error (got %q)", c.LogLevel)
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535 (got %d)", c.Port)
	}

	if len(c.Feeds) == 0 {
		return fmt.Errorf("at least one feed is required")
	}

	for i := range c.Feeds {
		feed := &c.Feeds[i]

		parsed, err := url.Parse(feed.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("feeds[%d].url must be an HTTP(S) URL (got %q)", i, feed.URL)
		}

		if feed.Interval.Duration() <= 0 {
			return fmt.Errorf("feeds[%d].interval must be positive", i)
		}

		if feed.Bluesky.Handle == "" {
			return fmt.Errorf("feeds[%d].bluesky.handle is required", i)
		}
		if feed.Bluesky.Password == "" {
			return fmt.Errorf("feeds[%d].bluesky.password is required", i)
		}
		if feed.Bluesky.PDS != "" {
			parsed, err := url.Parse(feed.Bluesky.PDS)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return fmt.Errorf("feeds[%d].bluesky.pds must be an HTTP(S) URL (got %q)", i, feed.Bluesky.PDS)
			}
		}

		if feed.MaxPostsPerRun == 0 {
			return fmt.Errorf("feeds[%d].max_posts_per_run must not be zero", i)
		}
	}

	return nil
}
