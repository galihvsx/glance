# glance — Design Specification

- **Date:** 2026-10-06
- **Status:** Draft — pending final review
- **Reference:** makeplane/plane (explored 2026-10-06, report in `~/workspace/plane-reference/EXPLORATION.md`) — design reference only, no code copied (Plane is AGPL)
- **License:** Apache-2.0

## 1. Problem & Thesis

Plane is the closest open-source Linear alternative, but it is heavy: ~12 deploy services (Django API, Celery worker + beat, Postgres, Valkey, RabbitMQ, MinIO, Hocuspocus live server, nginx, 3 frontends), Python performance overhead, and a non-trivial self-host setup. It also carries paid tiers.

**glance** = full Plane feature parity, lean implementation:

- **Lightweight:** single Go binary, single process, one Postgres. No Python, no Celery, no Redis, no RabbitMQ, no MinIO for v1.
- **Fast:** Go + Postgres-native types, performance budgets as acceptance criteria.
- **Trivial self-host:** download binary (or `docker compose up`), set `DATABASE_URL` + SMTP, running in < 5 minutes.
- **Fully open:** Apache-2.0, no pricing mechanism of any kind.
- **Quality bar:** compete with Jira/Linear on speed and UX — not "Plane, ported".

## 2. Decision Log

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | Product | Plane-like issue tracker, open-source, self-hosted | galih |
| 2 | Scope | Full Plane feature parity, lean implementation | galih |
| 3 | Architecture | Go monolith, single binary, React SPA embedded via `go:embed` | galih's proposal; best fit for thesis |
| 4 | Database | Postgres only, single dialect | galih; free providers (Neon, Supabase, Prisma) keep self-host easy; kills dual-dialect cost |
| 5 | Naming | Follow Plane's conventions (Intake, Cycle, Module, …) | galih |
| 6 | Plugin system | **Dropped** — no WASM/plugin runtime in v1 | galih ("ribet"); material simplification. Internal package boundaries stay clean so plugins remain possible later |
| 7 | Auth | Passwordless: email OTP + Google/GitHub OAuth. No passwords stored, ever | galih |
| 8 | License | Apache-2.0 | Jarvis recommended, galih accepted; trades commercial control for max distribution. Shield = "glance" trademark + NOTICE |
| 9 | v1 shape | Vertical slice: intake → triage → board → cycle + UI from day one | Jarvis recommended; thesis ("enak dipakai") can only be validated through UI |
| 10 | HTTP framework | Echo | galih's familiarity (omon-omon-api); no reason to switch |
| 11 | Frontend components | Strict shadcn (Base UI variant), default theme first, theming later | galih; velocity + coherence; anti-redundancy rule |
| 12 | Quality bar | Competitive bars as acceptance criteria (see §9) | galih: "jangan asal jadi" |

Rejected alternatives are recorded here deliberately: microkernel plugin arch (anti-YAGNI for v1), Go `.so` plugins (toolchain lock-in, no isolation), SQLite dual-dialect (rejected for single-dialect simplicity), magic link (deferred — needs email infra anyway, OTP covers it), custom design system for v1 (deferred to theming phase).

## 3. Architecture & Build Pipeline

```
glance/
├── cmd/glance/main.go        # single entrypoint
├── internal/
│   ├── api/                  # Echo handlers: /api/v1/*
│   ├── service/              # business logic per domain package
│   ├── store/                # Postgres persistence (single dialect)
│   ├── auth/                 # OTP + OAuth (passwordless)
│   ├── mail/                 # SMTP sender; log-to-stdout fallback in dev
│   └── realtime/             # in-process websocket hub
├── web/                      # React + Vite + TS → dist/ embedded
├── migrations/               # SQL, embedded, auto-run on start
├── Dockerfile                # single image: binary + migrations
├── docker-compose.yml        # glance + postgres (one-command self-host)
├── LICENSE                   # Apache-2.0
└── NOTICE                      # attribution (Apache-2.0 §4d)
```

- **Routing:** `/api/v1/*` → JSON API · `/ws` → websocket hub · `/*` → embedded SPA (catch-all serves `index.html` for client routing).
- **Build:** `npm run build` in `web/` → `go:embed web/dist` → `go build ./cmd/glance` → single binary (~20–30 MB incl. SPA assets).
- **Migrations:** embedded SQL, forward-only, auto-run on boot. Zero-downtime-safe by convention (expand-contract for risky changes).
- **Email:** `mail/` package; SMTP configured via env (`SMTP_HOST/PORT/USER/PASS/FROM`). Empty SMTP → log to stdout (dev). Required from v1 (OTP delivery).
- **Background work:** in-process ticker (cycle rollover, intake snooze expiry, archive automation) + transactional outbox dispatcher (webhooks, email). No external workers.
- **Known limitation (documented):** in-process realtime hub and ticker mean one instance. Horizontal scaling later = Redis pub/sub between instances. Not v1.

## 4. Data Model (Postgres-native)

Native types throughout: `UUID` PKs (`gen_random_uuid()`), `JSONB`, `TEXT[]`, `CITEXT`, `TSVECTOR` generated columns. No dialect abstraction.

### Auth (passwordless — no `password_hash` anywhere)

```
users             id UUID PK, email CITEXT unique, name, avatar_url, is_active, last_login_at
sessions          id UUID, user_id FK, token_hash, user_agent, ip, created_at, expires_at, revoked_at
otp_codes         id, email CITEXT, code_hash, attempts SMALLINT, expires_at (10 min), consumed_at
oauth_accounts    id, user_id FK, provider (google/github), provider_uid TEXT unique, tokens JSONB
```

### Core (Plane naming)

```
workspaces        id UUID PK, slug CITEXT unique, name, created_at
workspace_members workspace_id FK, user_id FK, role SMALLINT (20=admin/15=member/5=guest), PK(ws,user)
projects          id UUID PK, workspace_id FK, identifier VARCHAR(12) (ENG, unique per workspace),
                  name, description TEXT, view_flags JSONB, archive_in_days INT, close_in_days INT
states            id UUID PK, project_id FK, name, group (triage/backlog/unstarted/started/completed/cancelled),
                  color, sequence INT
issues            id UUID PK, project_id FK, sequence_id INT → display ID ENG-123,
                  name TEXT, description JSONB (TipTap doc), priority SMALLINT,
                  state_id FK, parent_id FK NULL (sub-issues), sort_order FLOAT8,
                  start_date DATE, target_date DATE, estimate_point_id FK NULL,
                  is_draft BOOL, archived_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
                  search TSVECTOR GENERATED (name + description text), created_by, timestamps
issue_sequences   project_id PK, last_value BIGINT — atomic increment via UPDATE..RETURNING
labels            id UUID PK, workspace_id FK, parent_id FK NULL (hierarchy), name, color
issue_labels      issue_id FK, label_id FK, PK both
issue_assignees   issue_id FK, user_id FK, PK both
cycles            id UUID PK, project_id FK, name, start_date, end_date, status, progress_snapshot JSONB
cycle_issues      cycle_id FK, issue_id FK, PK both
modules           id UUID PK, project_id FK, name, description TEXT, status, lead_id FK NULL
module_issues     module_id FK, issue_id FK, PK both
module_members    module_id FK, user_id FK, PK both
intake            id UUID PK, project_id FK, name, description TEXT, is_default BOOL
intake_issues     id, intake_id FK, issue_id FK, status SMALLINT
                  (pending/rejected/snoozed/accepted/duplicate), snoozed_till TIMESTAMPTZ,
                  duplicate_to_id FK NULL
```

### Satellites

```
issue_relations   issue_id FK, related_issue_id FK, type VARCHAR
                  (blocked_by/relates_to/duplicate/start_before/finish_before/implemented_by),
                  PK(issue_id, related_issue_id, type) — single canonical row; reverse derived in service layer
issue_activities  id, issue_id FK, actor_id FK, field TEXT, old_value JSONB, new_value JSONB,
                  created_at — append-only, field-level audit
issue_versions    id, issue_id FK, version_no INT, snapshot JSONB (full row),
                  created_by FK, created_at — history & restore
comments          id UUID PK, issue_id FK, parent_id FK NULL (threaded), actor_id FK, content JSONB
comment_reactions / issue_reactions / issue_votes / issue_subscribers — (entity, user) joins
notifications     id, user_id FK, type, title, payload JSONB, read_at TIMESTAMPTZ, created_at
notification_prefs user_id FK, event TEXT, in_app BOOL, email BOOL, PK(user_id, event)
webhooks          id UUID PK, workspace_id FK, url, secret, events TEXT[], active BOOL
outbox            id BIGSERIAL PK, event TEXT, payload JSONB, status, attempts INT,
                  next_retry_at TIMESTAMPTZ — webhooks + email go through here; in-process dispatcher
pages             id UUID PK, project_id FK, parent_id FK NULL (tree), title, content JSONB, sort_order FLOAT8
issue_views       id UUID PK, project_id FK, name, query JSONB (saved filter),
                  display_props JSONB, owner_id FK
api_tokens        id UUID PK, user_id FK, name, token_hash, scopes TEXT[], rate_limit INT,
                  expires_at, last_used_at — prefix gl_
estimates         id UUID PK, project_id FK, name
estimate_points   id, estimate_id FK, key TEXT, value INT, description TEXT
attachments       id UUID PK, issue_id FK, filename, mime, size BIGINT, storage_path TEXT,
                  created_by FK — local disk default, S3-compatible optional
favorites         user_id FK, entity_type TEXT, entity_id UUID, PK all
```

### High-regret decisions (locked)

1. **UUID PK + per-project sequence counter** — `ENG-123` is the user-facing identity; UUID is internal.
2. **Description as JSONB TipTap doc** — keeps the door open for collaborative editing without data migration.
3. **`sort_order` FLOAT8 + periodic rebalance** — simple drag-drop ordering.
4. **Soft delete** (`archived_at`/`deleted_at`) — free audit trail and undo.
5. **FTS via generated `tsvector`** — no Elasticsearch/Typesense needed.
6. **Single outbox** for webhooks + email — one async side-effect pattern.

## 5. API Design

One API for web + programmatic tokens (two auth methods, same handlers). Base `/api/v1/`.

### Auth endpoints

```
POST /api/v1/auth/otp/request        {email} → sends code (rate-limited)
POST /api/v1/auth/otp/verify         {email, code} → session cookie
GET  /api/v1/auth/oauth/{google|github}/login
GET  /api/v1/auth/oauth/{google|github}/callback
POST /api/v1/auth/logout
GET  /api/v1/me
POST /api/v1/ws/ticket               # single-use ticket for token-based ws auth
```

### Resource endpoints (nested, Plane-style)

```
GET/POST  /api/v1/workspaces/{slug}/projects
GET/PATCH /api/v1/workspaces/{slug}/projects/{identifier}/issues
GET       /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}
GET       /api/v1/work-items/ENG-123                                  # display-ID lookup
POST      /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk-update
POST      /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk-delete
.../states, .../labels, .../cycles, .../cycles/{id}/issues,
.../modules, .../intake, .../pages, .../views,
.../issues/{uuid}/comments, .../issues/{uuid}/relations,
.../issues/{uuid}/history, .../issues/{uuid}/versions
GET       /api/v1/search?q=                                            # tsvector global search
```

### Conventions

- **Cursor pagination** (`?cursor=&per_page=` → `{results, next_cursor}`). No offset — offset breaks on live-updating lists.
- **Delta sync:** `?updated_after=<ISO8601>` for lightweight polling clients.
- **Sparse fieldsets:** list endpoints omit `description` unless `?fields=description`.
- **Filtering:** `?state=&assignee=&label=&priority=&cycle=&q=` + `?order_by=-updated_at`, uniform across lists.
- **Errors:** consistent envelope `{error: {code, message, details}}` with correct HTTP status. Never 200-with-error-in-body.
- **Project addressing by identifier** (`ENG`), not UUID — deliberate, ergonomic deviation from Plane. UUIDs remain in response bodies.
- **`Idempotency-Key`** header on POST mutations — safe client retries, no duplicates.

## 6. Realtime

- **Endpoint `/ws`**, in-process hub. Web clients auth via session cookie; token clients fetch a single-use ticket (`POST /api/v1/ws/ticket`) and connect with `?ticket=`.
- **Channels:** `workspace:{slug}`, `project:{slug}:{identifier}` (workspace-qualified — project identifiers are unique per workspace, so the bare `project:ENG` scheme would leak events across workspaces sharing an identifier), `issue:{uuid}`, `user:{id}` (personal notifications). Client sends `{action:"subscribe", channel}`.
- **Events:** `issue.created/updated/deleted`, `comment.created`, `cycle.updated`, `intake.updated`, `notification.created`. Payload `{event, channel, data, at}`; `data` carries the changed entity or changed fields for optimistic reconciliation.
- **No replay buffer.** Reconnect → resync via `?updated_after=` delta endpoint (see §5).
- Last-writer-wins for concurrent edits in v1; CRDT collaborative editing deferred (version snapshots already cover history).

## 7. Auth Flows (passwordless)

- **Email OTP:** request → 6-digit code, 10-min expiry, max 5 attempts, rate limits (5/hr per email, 20/hr per IP). Code stored hashed. Verify → session cookie (`HttpOnly`, `Secure`, `SameSite=Lax`), code consumed. Unknown email → user auto-provisioned.
- **OAuth (Google/GitHub):** standard code flow via `x/oauth2` → callback → match/link account by verified email → session cookie.
- **Sessions:** opaque 32-byte token (`crypto/rand`), stored hashed. 30-day expiry, revoked on logout, list/revoke in settings.
- **CSRF:** no separate tokens — `SameSite=Lax` + Origin check on mutations (SPA is same-origin).
- **API tokens:** `gl_` prefix, stored hashed, scopes (`read`/`write`/`admin`), per-token rate limits.

## 8. Frontend

- **React + Vite + TS** SPA, built to `web/dist`, embedded via `go:embed`.
- **Strict shadcn (Base UI variant)** — the single component source. ALL shadcn components added upfront.
- **Anti-redundancy rule:** if shadcn covers it, use shadcn. New components only when shadcn cannot cover (composite views: kanban board, gantt).
- **Zero hardcoded tokens:** colors/fonts/spacing/radius via shadcn CSS variables only. Theming phase = swap the variable file, not components.
- **Reusability:** composites (issue card, state badge, priority picker) built on shadcn primitives in `web/components/`, single source used across views.
- **Server state:** TanStack Query (caching, dedup, optimistic updates; maps to cursor pagination + delta sync).
- **Board drag-drop:** dnd-kit. **Editor:** TipTap (JSON doc). **Command palette:** cmdk + global shortcut system (Cmd+K, `c`, `/`, full keyboard board navigation).
- **Routing:** React Router. Local UI state: zustand. **Realtime client:** ws with auto-reconnect + delta resync; optimistic-first rendering.

## 9. Competitive Bars (acceptance criteria)

### Performance budgets
- API list p95 < 100 ms (board with 500 issues)
- Issue detail < 300 ms · full-text search < 200 ms
- Binary cold start < 2 s · idle < 256 MB RAM

### UX bars (Linear-grade)
- Keyboard-first from day one: command palette, single-key shortcuts, board fully keyboard-navigable
- Optimistic UI on all mutations; rollback on failure — no spinners on every click

### Reliability bars
- Every mutation writes an `issue_activities` entry
- All side effects (webhooks, email) via outbox — none lost, none half-done
- Migrations forward-only and zero-downtime-safe

### Structural edges (unfair advantages)
- vs Jira: 5-minute deploy vs enterprise setup; raw speed
- vs Linear: self-hosted + Apache-2.0 + data ownership (Linear is closed-source SaaS)
- vs Plane: single binary vs 12 services; Go vs Python

## 10. v1 Scope — Vertical Slice

**In:** auth (OTP + OAuth), workspaces/projects, intake inbox + triage actions (accept/reject/snooze/duplicate), board (kanban) + list views, issue detail (comments, relations, history), states/labels/priorities/assignees, cycles list/detail, search, notifications (in-app), websocket live updates.

**Deferred (still on parity roadmap):** gantt (custom build, large work item), calendar view, pages/docs, roadmaps, analytics, spreadsheet view, modules (epics) UI, webhooks UI, public share boards, admin panel.

## 11. Post-v1 Roadmap

1. Remaining parity views (gantt, calendar, pages, roadmaps, analytics, spreadsheet)
2. Modules (epics) UI, webhooks UI, public boards, instance admin
3. Theming phase (glance visual identity on top of shadcn variables)
4. Horizontal scaling (Redis pub/sub for ws hub + ticker leader election)
5. Plugin system revisit (only on real community demand — internal boundaries stay clean)

## 12. Open Questions (non-blocking)

- "glance" trademark: use ™ consistently; consider formal registration later. Not blocking.
- CLA (Contributor License Agreement) before first external contribution — required to preserve future relicensing option. Not blocking v1.
- SMTP provider recommendation for self-hosters (docs task).

---

*Spec status: draft pending galih's review. No code until approved (hard gate).*
