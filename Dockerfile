FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/facebook-to-bluesky .

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata wget yt-dlp ffmpeg \
    && addgroup -S app \
    && adduser -S -G app app

COPY --from=build /out/facebook-to-bluesky /usr/local/bin/facebook-to-bluesky

USER app
WORKDIR /app

ENV CONFIG_PATH=/data/config.yml
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["facebook-to-bluesky"]
