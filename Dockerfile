# syntax=docker/dockerfile:1
#
# glance — lean, self-hosted issue tracker. Single Go binary + embedded SPA.
#
# Multi-stage build:
#   1. web-builder — Node builds the React SPA (web/dist)
#   2. go-builder  — Go compiles the server, embedding web/dist via go:embed
#   3. runtime     — distroless image with the single binary
#
# Verification note: no Docker daemon is available where this file is
# authored, so `docker build` has not been run against it. Every stage
# mirrors commands already verified locally (see README.md), and the
# COPY paths below were checked against the repo layout.

# ---------------------------------------------------------------------------
# Stage 1: build the React SPA
# Local equivalent: cd web && npm ci && npm run build
# ---------------------------------------------------------------------------
FROM node:24-bookworm-slim AS web-builder

WORKDIR /app/web

# Deps first — better layer caching when only web/src changes.
COPY web/package.json web/package-lock.json ./
RUN npm ci

# Build the SPA. Output: web/dist (consumed by the go stage below).
COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: build the Go server
# Local equivalent (PATH=/usr/local/go/bin, GOTOOLCHAIN=local):
#   go mod download && go build -o glance ./cmd/glance
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS go-builder

WORKDIR /app

ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    GOCACHE=/tmp/gocache \
    GOTMPDIR=/tmp/gocache

# Module deps first — layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Application source (web/dist arrives separately from the node stage).
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
COPY web/embed.go ./web/
# go:embed (web/embed.go) requires web/dist at COMPILE time.
COPY --from=web-builder /app/web/dist ./web/dist

RUN go build -trimpath -ldflags="-s -w" -o /glance ./cmd/glance

# ---------------------------------------------------------------------------
# Stage 3: minimal runtime
# distroless/base (not static) ships CA certificates — Phase 1 adds SMTP
# (STARTTLS) and OAuth HTTPS calls, which need them.
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/base-debian12:nonroot

COPY --from=go-builder /glance /glance

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/glance"]
