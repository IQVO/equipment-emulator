# syntax=docker/dockerfile:1.7

# --- build stage ---
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
WORKDIR /src

# Cache go.mod/go.sum download separately from source so editing source
# code doesn't bust the module-download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build EVERY cmd/* binary by looping over the cmd/ directories, so a new
# composition root can never be merged with a Dockerfile that silently forgets
# to build it (the first symptom would otherwise be a CrashLoopBackOff on
# `exec: "/app/<binary>": no such file or directory`). Each binary lands in
# /out/<dirname>; the chart picks one with `command: ["/app/<dirname>"]`.
# BuildKit cache mounts for the module and build caches speed up repeat
# builds in CI without baking the cache into the image layers.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eu; \
    mkdir -p /out; \
    for d in cmd/*/; do \
        name="$(basename "$d")"; \
        echo "building ${name}"; \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o "/out/${name}" "./cmd/${name}"; \
    done

# --- runtime stage ---
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 -S app && adduser -u 1000 -S app -G app
WORKDIR /app
# The emulator writes nothing to disk, so the chart mounts the root filesystem
# read-only. Logs go to stdout. The example world is the default; the chart
# mounts its own world.yaml over /etc/emulator.
COPY --from=build --chown=app:app /out/ ./
COPY --from=build --chown=app:app /src/config/world.example.yaml /etc/emulator/world.yaml
ENV WORLD_FILE=/etc/emulator/world.yaml
USER 1000
EXPOSE 8080
# cmd/emulator is the only binary (admin API on :8080).
ENTRYPOINT ["./emulator"]
