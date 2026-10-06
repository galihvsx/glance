# glance

A lean, self-hosted issue tracker — full Plane-style workflow (intake → triage → board → cycles) in a **single Go binary** with an embedded React SPA. No Python, no microservices, no heavy setup.

Apache-2.0. Built to be boring to deploy and fast to run.

## 5-minute quickstart

**Prereqs:** Docker (recommended) — or Go 1.25+, Node 20+, Postgres 14+ for local dev.

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
export PATH=/usr/local/go/bin:$PATH   # if go isn't on your PATH
go build -o glance ./cmd/glance

# 3. Point at Postgres and run (migrations run automatically)
export DATABASE_URL=postgres://glance:glance@localhost:5432/glance?sslmode=disable
./glance
```

Open http://localhost:8080.

## Configuration

All config is via environment variables:

| Variable        | Required | Default | Description                                              |
|-----------------|----------|---------|----------------------------------------------------------|
| `DATABASE_URL`  | yes      | —       | Postgres connection string                               |
| `PORT`          | no       | `8080`  | HTTP listen port                                         |
| `APP_URL`       | no       | —       | Public base URL (used for links in emails)               |
| `SMTP_HOST`     | no       | —       | SMTP host for outbound mail                              |
| `SMTP_PORT`     | no       | —       | SMTP port                                                |
| `SMTP_USER`     | no       | —       | SMTP username                                            |
| `SMTP_PASSWORD` | no       | —       | SMTP password                                            |
| `SMTP_FROM`     | no       | —       | From address                                             |

When SMTP is unset, outbound mail falls back to logging (dev-friendly).

## Layout

```
cmd/glance/        server entrypoint
internal/
  config/          env-based configuration
  store/           pgx pool + embedded migration runner
  api/            Echo routes (/api/v1, /ws, SPA catch-all)
web/               React SPA (Vite + TS + Tailwind v4 + shadcn/Base UI)
migrations/        SQL migrations, applied in order at startup
spec/              design spec (binding authority for the build)
plans/             implementation plan
```

`web/dist` is a build artifact (gitignored) — build it before `go build`, or use the Dockerfile which does both stages.

## Docs

- Design spec: [`spec/`](spec/)
- Implementation plan: [`plans/`](plans/)
- Legal: [`LICENSE`](LICENSE) (Apache-2.0), [`NOTICE`](NOTICE)

## License

Apache License 2.0 — see [LICENSE](LICENSE).
