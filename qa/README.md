# Visual QA harness

Headless screenshot harness for visual regression / UI-polish checks. Builds the
web app + Go binary, boots a scratch Postgres database, logs in via OTP, seeds a
fixed fixture set, and screenshots any combination of screens with Playwright
(headless Chromium).

## Prerequisites

- Node ≥ 20 (for `playwright-core`)
- `psql` on PATH and a reachable Postgres with a role that can create databases
- A Chromium binary — see "Chromium" below
- A built repo checkout (`visual.sh` builds `<worktree-path>/web` and `./cmd/glance`)

## Setup (once)

```bash
cd qa && npm ci
```

## Running

From the repo root:

```bash
./qa/visual.sh /path/to/glance/checkout board detail
```

Screens: `shell | board | detail | list | mywork | inbox`.
Output lands in `qa/shots/<screen>.png` (gitignored — never committed).

Example detail flow:

```bash
./qa/visual.sh ~/workspace/glance board
# detail-shot/detail-interact need a running instance + session:
node qa/detail-shot.mjs http://127.0.0.1:18080 <session-cookie> <issue-uuid> qa/shots/detail-uuid.png
node qa/detail-interact.mjs http://127.0.0.1:18080 <session-cookie> <issue-uuid> qa/shots
```

`visual.sh` prints the login cookie value and uses a unique scratch database
`glance_qa_visual_<pid>` per run, so parallel runs never clobber each other.
Build logs and the server log go to `/tmp/qa-*.log`.

## Credentials (env only — nothing secret is committed)

| Variable | Default | Meaning |
| --- | --- | --- |
| `GLANCE_QA_DB_USER` | `glance` | Postgres role |
| `GLANCE_QA_DB_PASSWORD` | `glance` | Role password (**local-dev default only**; set a real value for shared/CI runs) |
| `GLANCE_QA_DB_HOST` | `127.0.0.1` | Postgres host |
| `GLANCE_QA_DB_PORT` | `5432` | Postgres port |
| `PLAYWRIGHT_CHROMIUM_PATH` | `/home/hatch/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome` | Chromium binary for screenshots |

## Adding a new screen

Add one entry to the `routes` map in `shot.mjs` (path is the SPA route, e.g.
`/w/acme/p/EXAMP/board` — seeded by `visual.sh` with workspace slug `acme`,
project identifier `EXAMP`, and the 7 fixture issues), then name it in
`visual.sh`'s usage comment and pass it on the command line.

## Chromium

`playwright-core` does not download a browser. The default path above is this
dev VM's cached build; on another machine run `npx playwright install chromium`
inside `qa/` and export `PLAYWRIGHT_CHROMIUM_PATH` to the installed binary.

## Seeded fixtures

`visual.sh` seeds a deterministic workspace: `acme` / project `EXAMP` with 7
issues across Backlog/Todo/In Progress/Done. Seed data is synthetic example
titles — no real user data, no credentials.
