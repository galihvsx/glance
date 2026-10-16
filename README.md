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

## Features

| Area | What's in |
|---|---|
| **Issues** | CRUD with per-project sequences (`ENG-123`), sub-issues with progress rollup, priorities, labels, estimates, assignees, watchers, full-text search, filters, cursor pagination, bulk operations, idempotency keys, duplicate detection on create, blocker guard on completion, clone, archive, time tracking |
| **Views** | List, Kanban board (drag & drop, keyboard-operable), Calendar (month/week drag, quick-add, unscheduled strip), Spreadsheet (inline cell editing, custom-field columns), Gantt (dependency editing), Roadmap, saved views (own + shared), global search + `Cmd/Ctrl+K` palette |
| **Intake** | Inbox — accept to backlog, reject, snooze, mark duplicate, bulk triage, AI triage suggestions |
| **Cycles** | Sprints with progress tracking, automatic rollover, cycle-ending scheduled automations |
| **Modules & Releases** | Modules for grouping work; Releases (milestones) with live progress snapshot + release notes |
| **Pages** | Wiki pages with comments, Mermaid diagrams, two-way links to work items, create issue from a page |
| **Initiatives** | Programs grouping projects, with aggregate rollup + completion % |
| **OKRs / Goals** | Objectives + measurable key results (metric + issue-linked), check-in history with progress chart + list sparkline, live updates |
| **Workload & Risk** | Per-member load vs weekly capacity, overload alerts + weekly digest section; risk radar composite score (overdue / stalled / unassigned / overload) |
| **Recurring** | Scheduled issue creation (daily/weekly/monthly) with run-now |
| **Automations** | Event + scheduled triggers (13 event types), 12 actions, run history, retention, blocker-safe |
| **AI assistance** | Draft-with-AI, triage suggestions, issue summary, sub-issue breakdown, duplicate merge — provider-agnostic, degrades honestly when unconfigured |
| **Realtime** | WebSocket live updates on every authenticated route (global + workspace + project scopes), single connection indicator |
| **Auth & roles** | Passwordless email OTP + Google/GitHub OAuth, cookie sessions, scoped API tokens, viewer/member/admin roles, workspace invites |
| **Admin & audit** | Instance admin panel (users, workspaces, stats), append-only workspace audit log + CSV export, admin audit log |
| **Importers** | GitHub, Jira Cloud, Trello, Linear, Plane JSON export — preview + mapping, honest-loss reports |
| **Export & portability** | Issue CSV/JSON export, full workspace archive export + idempotent re-import |
| **Integrations** | Webhooks (HMAC, retry + backoff, delivery log, SSRF guard), Slack notifications, GitHub two-way PR linking, due-date reminders, stale-issue nudges |
| **Notifications** | In-app inbox + @mentions, per-user prefs, email digests (per-user timezone), live bell |
| **Custom fields & templates** | Project custom fields (text/number/date/select/checkbox, required enforcement), issue templates, project templates |
| **Attachments & sharing** | Issue attachments (local storage, 25 MB default cap), public share links |

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

| | glance (main) | Plane |
|---|---|---|
| Issue tracking, boards, cycles, intake | ✅ | ✅ |
| Self-host difficulty | one binary + Postgres | multiple services |
| Idle memory | ~15MB | GBs |
| Realtime collaboration | ✅ (WebSocket, live on every route) | ✅ |
| Pages / docs | ✅ | ✅ |
| Gantt, calendar, analytics | ✅ | ✅ |
| Importers (GitHub/Jira/Trello/Linear/Plane) | ✅ | ✅ |
| Initiatives, Automations, Custom fields | ✅ (free, no paywall) | ✅ (paywalled) |
| Recurring issues, Risk radar, Workload capacity | ✅ (beyond Plane) | — |

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

Everything in the old roadmap has shipped — pages, Gantt, calendar, analytics,
importers, admin panel, favorites, attachments, saved views.

What's still ahead: the **v0.2.0** and **v0.3.0** releases — see
[CHANGELOG.md](CHANGELOG.md) for what's already on `main` in each. Release
publishes are a manual checkpoint, never automatic.

## Development

```bash
# backend tests (needs Postgres)
export TEST_DATABASE_URL=postgres://glance:glance@127.0.0.1:5432/glance_test?sslmode=disable
go test ./...

# flake sweep — run the suite N times to catch flaky tests (see docs/FLAKES.md)
./qa/flake-sweep.sh 3 ./...

# frontend
cd web && npm ci && npx tsc --noEmit && npm run build

### Visual QA harness

```bash
cd qa && npm ci && cd ..
./qa/visual.sh /path/to/glance/checkout board detail
```

Builds web + the Go binary, boots a scratch Postgres DB, logs in via OTP, seeds a
fixed fixture set, and screenshots the named screens to `qa/shots/`. DB creds come
from `GLANCE_QA_DB_*` env vars (local-dev defaults in `qa/README.md`). See
[`qa/README.md`](qa/README.md) for screens, adding new screens, and CI-safe runs.
```

See [CHANGELOG.md](CHANGELOG.md) for release notes, [docs/perf.md](docs/perf.md) for the performance methodology, and [docs/FLAKES.md](docs/FLAKES.md) for the known-flakes register and the `qa/flake-sweep.sh` flake sweep (3 repeated `go test` runs — run it when a test smells flaky).

## License

Apache-2.0 — see [LICENSE](LICENSE).
