# Importing issues from Plane

glance can import a project from a **Plane JSON export** into a glance
project. The flow is two-phase — *analyze, then execute* — and the import
report tells you honestly what didn't survive. Nothing is silently guessed.

## 1. Export from Plane

In Plane: **Settings → Exports → JSON**. Download the export and keep it as
the raw `.json` file or as the `.zip` Plane gives you (the importer accepts
both — a `.zip` must contain exactly one `.json`).

Rules the importer enforces up front:

- **One Plane project per file.** The export must come from a single Plane
  project; one import run maps it to one glance project. A file whose rows
  carry more than one `project_identifier` is rejected — go back to Plane
  and export each project separately.
- **The export is a bare JSON array** (no version envelope — that's just how
  Plane emits it). A wrapped object or anything else is rejected as a shape
  error, not imported half-way.
- **256 MiB cap** on the input (both raw and zipped), so a zip bomb can't
  eat the server. Anything larger is refused before parsing.
- **Row failures are per-row, never fatal.** A malformed row (wrong type,
  missing name or identifier) is skipped and reported under its Plane
  `identifier` — the rest of the file still imports. You get the full
  error list in the report.

No migration is involved: this importer reads the file and writes issues
through the normal issue path. Each imported issue records its Plane
identifier, so the report can be correlated back to the source rows —
run the same export twice and you get two copies; fix problems in one
pass and re-run only with a corrected file whose already-imported rows
were removed (the row errors tell you exactly which those are).

## 2. Analyze → map → execute

Open **Project settings → Import**, choose **Plane**, and upload the file.

**Analyze** parses the whole export and shows you everything it can't
resolve on its own:

- **People** — Plane's export carries assignees and comment authors as
  plain *name strings* (no emails, no user IDs). You map each name to a
  workspace member, explicitly, one by one. Unmapped names stay unassigned
  and are recorded as gaps — never guessed.
- **States** — Plane state names are matched case-insensitively against
  your project's states; unmatched names are created (in the `unstarted`
  group, the neutral "tracked, not yet worked" bucket).
- **Labels, cycles, modules** — same matched-or-created rule as states.
  New labels get the default gray (Plane exports no colors for them).

**Execute** imports in a single database transaction: all-or-nothing.
Afterward you get a per-issue report with a `gaps` list — exactly what
didn't survive, issue by issue, so you can audit the migration instead of
trusting it.

## 3. What survives, honestly

Survives:

- **Identifiers and names.** Every Plane identifier (`PROJECT-123`) is
  kept as a stamp on the imported issue, so you can trace anything back
  to the source.
- **Priorities.** Mapped onto glance's priority scale.
- **Dates.** Start dates, target dates, and the original created/updated
  timestamps — including per-comment timestamps (Plane exports comment
  times as naive local stamps; the importer reads them as **UTC**, which
  is how Plane's own API serializes them).
- **Comments**, with authorship and original timestamps preserved.
- **Parent links.** Sub-issues re-link to their parents *when the parent
  is in the same export*. A parent identifier that isn't in the file
  ("dangling parent") becomes a root issue instead, with a warning in
  that issue's `gaps` — we import the work rather than drop it.

Does **not** survive:

- **Descriptions. Plane never exports them.** This is the biggest loss and
  it's upstream's doing, not ours: Plane's JSON export simply has no
  description column. There is nothing to import. If descriptions matter,
  copy them out of Plane before you migrate.
- **Issue-to-issue relations** (blocked-by, duplicates, ...). glance has
  no issue-relations model; relations are dropped and recorded per issue
  in `gaps`.
- **Attachment files.** The export carries only a *count* of attachments
  per issue — no files, no URLs. The count shows up in the issue's gaps;
  upload any files that matter yourself after the import.
- **Unset estimates.** Plane emits estimates as a number *or* an empty
  string in the same file; unset means no estimate in glance, nothing
  fudged.
- **Dangling parents** (above): parent missing from the file → root
  issue + warning.

## 4. Clean up triage, drafts, and archived issues first

Plane's export includes **everything** in the project — the working board,
the triage inbox, drafts, and archived issues. The importer takes rows
at face value: whatever states are in the export get matched-or-created
in glance, so a messy triage pile and a stack of old archived issues
become your problem in the new project.

Do this in Plane **before** exporting:

1. Move real work out of triage into a real state.
2. Resolve or delete drafts (`is_draft` rows are flagged by the analyze
   step, but it's cheaper to clean them at the source).
3. Decide what archived issues you actually want — the rest won't come
   back; leave them behind.

If anything slips through, the analyze step flags every draft and
archived row explicitly, and the import report's `gaps` will note where
they landed. Nothing sneaks in silently either way.

## 5. Limits worth knowing

- The importer never guesses people: **unmapped names stay unassigned**,
  recorded as gaps.
- Unknown extra fields in the export are tolerated (Plane's column set
  drifts between versions); only wrongly *typed* fields fail a row.
- The import report records each row's Plane identifier — use it to audit
  what landed. Imports are one-way and additive: don't run the same file
  twice.
