[![CI](https://github.com/shi-gg/facebook-to-bluesky/actions/workflows/ci.yml/badge.svg)](https://github.com/shi-gg/facebook-to-bluesky/actions/workflows/ci.yml)
![](https://img.shields.io/github/repo-size/shi-gg/facebook-to-bluesky?maxAge=3600)

## About
Facebook to Bluesky mirrors Facebook pages to Bluesky using [FetchRSS](https://fetchrss.com) feeds. It is a small Go server that polls any number of RSS feeds and publishes new items to the Bluesky account configured for each feed.

**You can use the following features**
- Multiple feeds, each with its own Bluesky account and polling interval
- Plain text, links, hashtags and up to four images per post
- No database or Redis: the PDS is the source of truth
- Health and status endpoints for container monitoring
- Environment variable expansion for secrets

```yml
feeds:
    - name: ÖBB
      url: https://fetchrss.com/feed/1xTEpA2Tj6Es1xTEoH64M9KM.rss
      interval: 15m
      bluesky:
          handle: your-handle.bsky.social
          password: ${BLUESKY_APP_PASSWORD}
```

A feed is polled immediately on startup and then every `interval`. Items older than `max_age` are skipped, and at most `max_posts_per_run` posts are published per poll. Items are published oldest first.

Duplicate posts are prevented without a database. Every record is created with a record key derived from the RSS item ID (`x-facebook-id`), so a `com.atproto.repo.getRecord` lookup against the PDS is enough to know whether the post already exists. The full source ID is also stored in the record as `x-facebook-id`.

If you delete a post on Bluesky, it may be published again on the next poll. Removing the record removes the deduplication marker.

## Setup & Usage

### Getting the credentials
Create an [app password](https://bsky.app/settings/app-passwords) for the account that should publish the posts. Use the app password, not your account password.

Then create an RSS feed for a Facebook page on [FetchRSS](https://fetchrss.com). The feed URL goes into the `url` field of the configuration.

### Docker Deployment
To deploy Facebook to Bluesky with Docker, create a `config.yml` based on [`config.example.yml`](config.example.yml) and a `compose.yml` like the following.

`compose.yml`:
```yml
services:
    facebook-to-bluesky:
        image: ghcr.io/shi-gg/facebook-to-bluesky:latest
        container_name: facebook-to-bluesky
        restart: unless-stopped
        ports:
            - "8080:8080"
        volumes:
            - ./config.yml:/data/config.yml:ro
        environment:
            CONFIG_PATH: /data/config.yml
            BLUESKY_APP_PASSWORD: ${BLUESKY_APP_PASSWORD}
        logging:
            driver: json-file
            options:
                max-size: "10m"
                max-file: "3"
```

Store the app password in `.env` so it does not end up in `config.yml`:
```bash
echo "BLUESKY_APP_PASSWORD=your-app-password" >> .env
```

To start the server, run:
```bash
docker compose up -d
```

To update the server, pull the latest image and restart the container:
```bash
docker pull ghcr.io/shi-gg/facebook-to-bluesky:latest
docker compose restart facebook-to-bluesky
```

### Binary Deployment
Alternatively, build the binary from source. Go 1.27 or newer is required.

```bash
go build -o facebook-to-bluesky .
CONFIG_PATH=config.yml ./facebook-to-bluesky
```

The server listens on port `8080` by default and can be stopped with `SIGINT` or `SIGTERM`.

## Configuration
`config.yml`:
```yml
log_level: info
port: 8080

feeds:
    - name: ÖBB
      url: https://fetchrss.com/feed/1xTEpA2Tj6Es1xTEoH64M9KM.rss
      interval: 15m
      max_age: 24h
      max_posts_per_run: 5
      include_link: true
      bluesky:
          handle: your-handle.bsky.social
          password: ${BLUESKY_APP_PASSWORD}
          # pds: https://bsky.social

    - name: Another page
      url: https://fetchrss.com/feed/another-feed.rss
      interval: 1h
      bluesky:
          handle: did:plc:abcdefghijklmnopqrstuvwxyz
          password: ${SECOND_APP_PASSWORD}
          pds: https://bsky.social
```

| Key | Type | Default | Description |
|---|---|---|---|
| `log_level` | string | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `port` | int | `8080` | HTTP port for the health and status endpoints |
| `feeds` | list | — | One or more feed definitions |
| `feeds[].name` | string | feed URL | Name used in logs and the status endpoint |
| `feeds[].url` | string | — | RSS or Atom feed URL |
| `feeds[].interval` | duration | `15m` | How often the feed is polled |
| `feeds[].max_age` | duration | `24h` | Items older than this are skipped; a negative value disables the limit |
| `feeds[].max_posts_per_run` | int | `5` | Maximum posts published per poll; a negative value disables the limit |
| `feeds[].include_link` | bool | `true` | Append the original post URL to the Bluesky post |
| `feeds[].bluesky.handle` | string | — | Handle or DID of the target account |
| `feeds[].bluesky.password` | string | — | App password for the target account |
| `feeds[].bluesky.pds` | string | resolved | PDS host override, for example `https://bsky.social` |

The following environment variables can be set for the server.

| Variable | Type | Default | Description |
|---|---|---|---|
| `CONFIG_PATH` | string | `config.yml` | Path to the YAML configuration file |
| `PORT` | int | config value | Overrides `port` from the configuration |

All `${VAR}` placeholders in the configuration file are expanded from the environment before parsing, which is useful for passwords and Docker secrets.

## Endpoints
| Endpoint | Description |
|---|---|
| `GET /healthz` | Returns `{"status":"ok"}` while the process is running |
| `GET /status` | Returns the last poll time, last error, published/skipped counts and last post URI for every feed |

`/status` never exposes handles, passwords or other credential details.

## Development
```bash
go vet ./...
go test ./...

docker build -t facebook-to-bluesky .
```

Images are downloaded fresh on every publish. FetchRSS image URLs from Facebook expire after a while, so old feed items whose images are no longer reachable are published as text-only. Images that fail to download, exceed 2 MB or use an unsupported format are skipped individually. Video posts are published as text with the video thumbnail and a link to the original post.

Bluesky limits posts to 300 graphemes. Longer Facebook posts are truncated with an ellipsis, while room is reserved for the source link.
