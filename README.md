# glance

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev)
[![Release](https://img.shields.io/github/v/release/galihvsx/glance)](https://github.com/galihvsx/glance/releases)

A lean, self-hosted issue tracker — the full Plane-style workflow (intake → triage → board → cycles) in a **single Go binary** with an embedded React SPA. No Python, no microservices, no heavy setup.

![Kanban board](docs/screenshots/board.png)

## Why glance?

Plane is powerful, but it's heavy: Python backend, many moving parts, hungry for resources, fiddly to self-host. **glance** keeps the workflow you actually use and throws away the operational weight:

- **One binary** — Go server + embedded React SPA, serves everything on one port
- **Postgres-only** — no Redis, no extra services; migrations run automatically at startup
- **Boring to deploy** — `docker compose up`, done
- **Fast** — issue list p95 3.5ms, cold start <0.2s, idle RSS ~15MB ([benchmarks](docs/perf.md))

## Features (v0.1.0)

| Area | What's in |
|---|---|
| **Issues** | CRUD with per-project sequences (`ENG-123`), sub-issues, priorities, labels, assignees, full-text search, filters, cursor pagination, bulk operations, idempotency keys |
| **Views** | List, Kanban board (drag & drop, keyboard-operable), issue detail with comments, relations, and full history |
| **Intake** | Inbox for incoming issues — accept to backlog, reject, snooze, or mark duplicate |
| **Cycles** | Sprints with progress tracking and automatic rollover |
| **Realtime** | WebSocket live updates, command palette (`⌘K`), keyboard shortcuts throughout |
| **Auth** | Passwordless email OTP + Google/GitHub OAuth, cookie sessions, scoped API tokens |
| **Integrations** | Webhooks with HMAC signatures, retry + backoff, SSRF guard |

![Issue list](docs/screenshots/list.png)
![Intake inbox](docs/screenshots/inbox.png)
![Cycles](docs/screenshots/cycles.png)

## 5-minute quickstart

**Prereqs:** Docker (recommended) — or Go 1.26+, Node 20+, Postgres 14+ for local dev.

`OTP_PEPPER` is **required** — the server refuses to boot without it (fail closed).
Generate one first, or copy `.env.example` to `.env` (compose reads it automatically):

```bash
export OTP_PEPPER=$(openssl rand -base64 32)
```

### Option A: Docker (easiest)

```bash
docker compose up --build
```

Open http://localhost:8080. The server runs its own DB migrations on startup — nothing to set up manually.

### Option B: local dev

```bash
# 1. Build the SPA (required before the Go build — it's embedded in the binary)
cd web && npm ci && npm run build && cd ..

# 2. Build the server
go build -o glance ./cmd/glance

# 3. Point at Postgres and run (migrations run automatically)
export DATABASE_URL=postgres://glance:glance@localhost:5432/glance?sslmode=disable
export OTP_PEPPER=$(openssl rand -base64 32)
./glance
```

Open http://localhost:8080.

## Configuration

All config is via environment variables — see [`.env.example`](.env.example) for the full list with docs:

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | Postgres connection string |
| `OTP_PEPPER` | yes | — | Server-side secret for OTP hashes; server refuses to boot without it |
| `PORT` | no | `8080` | HTTP listen port |
| `APP_URL` | no | — | Public base URL (used for links in emails) |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `SMTP_FROM` | no | — | Outbound mail; when unset, mail falls back to logging (dev-friendly) |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | no | — | Google OAuth login |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | no | — | GitHub OAuth login |
| `OAUTH_STATE_SECRET` | no | — | Signs OAuth state (required when OAuth is configured) |

## glance vs Plane

| | glance v0.1.0 | Plane |
|---|---|---|
| Issue tracking, boards, cycles, intake | ✅ | ✅ |
| Self-host difficulty | one binary + Postgres | multiple services |
| Idle memory | ~15MB | GBs |
| Realtime collaboration | ✅ (WebSocket) | ✅ |
| Pages / docs | roadmap | ✅ |
| Gantt, calendar, analytics | roadmap | ✅ |
| Importers (GitHub/Jira) | roadmap | ✅ |

glance is for teams that want Plane's core workflow without Plane's operational footprint.

## Architecture

```
┌─────────────────────────────────────────┐
│  glance (single Go binary)              │
│  ┌──────────┐  ┌──────────────────────┐ │
│  │ Echo API │  │ React SPA (embedded) │ │
│  │ /api/v1  │  │ served from /        │ │
│  │ /ws      │  │                      │ │
│  └────┬─────┘  └──────────────────────┘ │
│       │ pgx                             │
└───────┼─────────────────────────────────┘
        ▼
   PostgreSQL (only dependency)
```

- `cmd/glance/` — server entrypoint
- `internal/` — config, pgx pool + embedded migration runner, API handlers, services (issues, intake, cycles, notifications, webhooks, mail)
- `web/` — React SPA (Vite + TS + Tailwind v4 + shadcn/Base UI)
- `migrations/` — forward-only SQL migrations, applied in order at startup
- `spec/` — design spec (binding authority for the build)

`web/dist` is a build artifact (gitignored) — build it before `go build`, or use the Dockerfile which does both stages.

### API docs

The full REST API is documented in [`docs/openapi.yaml`](docs/openapi.yaml) (OpenAPI 3.0) — every route, auth scheme (session cookie vs scoped API token), and request/response shape. A running server also serves the same document live at `GET /api/v1/openapi.json` (no auth). A route-coverage test (`internal/api/openapi_test.go`) fails the build if a registered route is missing from the spec, so the two cannot drift.

## Roadmap

Post-v1, each ships as its own forward-only migration + feature plan: **pages**, **attachments**, **issue views** (saved filters), **Gantt & calendar**, **roadmaps**, **analytics**, **public boards**, **admin panel**, **importers** (GitHub/Jira), **favorites**.

## Development

```bash
# backend tests (needs Postgres)
export TEST_DATABASE_URL=postgres://glance:glance@127.0.0.1:5432/glance_test?sslmode=disable
go test ./...

# frontend
cd web && npm ci && npx tsc --noEmit && npm run build
```

See [CHANGELOG.md](CHANGELOG.md) for release notes and [docs/perf.md](docs/perf.md) for the performance methodology.

## License

Apache-2.0 — see [LICENSE](LICENSE).
