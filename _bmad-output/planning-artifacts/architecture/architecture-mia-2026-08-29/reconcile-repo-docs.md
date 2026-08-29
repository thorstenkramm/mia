# Reconciliation: ARCHITECTURE-SPINE.md vs repo docs

Sources checked: `docs/architecture.md`, `docs/jobs.md`, `docs/database-layout.md` (skim:
ID prefixes, ownership, transactions), `docs/data-dir.md`, `AGENTS.md` (Go/architecture/testing rules).

Verified consistent (not repeated below): Go ≥1.27.0 and module path; Echo 5.3.1;
`modernc.org/sqlite` with fixed `data_dir/mia.sqlite3`, WAL, `synchronous=FULL`, foreign keys,
5-second busy timeout, 4 open / 4 idle, no reader/writer split; lock-before-SQLite ordering;
embedded forward-only `golang-migrate` v4 with dirty/newer refusal; all six spine ID prefixes
(`u_`, `cou_`, `mat_`, `ts_`, `job_`, `aud_`) match `docs/database-layout.md`; 2-minute lease with
30-second renewal and ≤3 attempts; 16 KiB / 1 second streaming persistence batches; speech
generating/available/failed lifecycle with shared generation and explicit retry; startup order
(expired speech → stranded generation failed → fatal missing-file check → orphan reconciliation);
missing avatars/logos normal; uniqueness-key conventions (ASCII lowercase, `cases.Fold`+NFC, no
`NOCASE`); boolean/enum/JSON SQL conventions; instant format (6 fractional digits persisted,
≤9 accepted); adapter allowlisting, no request-path auto-retry, local-only startup validation;
`mistral-ocr-4-1`; tiktoken `o200k_base`; SecLists top-100k embedded.

## Findings (by severity)

### F1 — HIGH — AD-5 contradicts the jobs terminal-failure contract

Spine AD-5 states the jobs system does "requeue on shutdown/expired lease" unconditionally.
`docs/jobs.md` (Execution policy) says: requeue only **when attempts remain** and the interrupted
attempt **stays counted**; when no attempt remains, MIA performs **the job subject's documented
terminal-failure transition in the same transaction** as the failed job state (e.g. material →
`failed` + cancel remaining material-owned jobs, per `docs/database-layout.md` `jobs`
constraints). As written, a slice could implement unconditional requeue of exhausted jobs, or
fail the job without the coupled subject transition. AD-5 (or AD-6) should carry both halves:
attempts-remaining condition and the same-transaction subject terminal transition.

### F2 — MEDIUM — Structural seed pre-creates packages beyond the confirmed initial scope

`docs/architecture.md` ("Initial scaffolding is limited to command wiring, configuration, process
locking, SQLite and migrations, identity validation, the HTTP server, and the first authentication
slice") and AGENTS.md ("Initial packages cover only command wiring, configuration, locking,
SQLite/migrations, identity, HTTP, and the first auth slice; add feature packages only with their
implementation") confirm a minimal initial tree. The spine's Structural Seed defers only *feature*
packages while showing `jobs/`, `audit/`, and all five `provider/*` adapters as unconditional
kernel entries. `audit`, `provider/smtp`, and `provider/clicksend` are plausibly required by the
auth slice (audit events, recovery email, SMS MFA), but `jobs/`, `provider/openai`, and
`provider/mistral` are not. The seed should mark kernel packages as created on first need, or the
spine invents an initial-creation decision the repo settled differently.

### F3 — MEDIUM — Filesystem permission contract absent from the spine

`docs/data-dir.md` fixes: data directory owned by the service user with no group/other bits;
internal directories mode `0700`; files mode `0600` (lock file, `session.key`, speech temp files
all explicitly `0600`). Multiple feature slices write files (material, speech, avatars, logos),
so this is a cross-slice rule, yet AD-11 and the conventions table say nothing about modes. Two
slices can diverge (e.g. one publishing `0644` output). Add a one-line convention.

### F4 — MEDIUM — File-deletion ordering contract absent from the spine

AD-11 covers publication (atomic rename before/with DB commit) but not deletion.
`docs/database-layout.md` (Attribution and deletion) fixes the confirmed rule: SQLite and
filesystem changes do not commit atomically; a deletion first makes the file/tree unreachable in
SQLite, then attempts synchronous filesystem deletion; a rare orphan is accepted and removed by
startup reconciliation; managed-file deletion failure is logged (and is an audited event family).
Material, speech, avatar, logo, and course-directory deletion all need this ordering; without it
in the spine, a slice may delete files before the DB commit and lose data on rollback.

### F5 — MEDIUM — Bearer-token persistence convention absent from the spine

`docs/database-layout.md` (Bearer tokens, `mfa_management_proofs`, `mfa_recovery_codes`) and
AGENTS.md confirm: invitation and password-reset tokens are canonical lowercase UUID v4;
persistence stores only the 32-byte SHA-256 digest (BLOB); plaintext exists only while
constructing the link/response and is never persisted, logged, or audited. This spans at least
the invitation and auth slices. The spine's Redaction row bans logging tokens but says nothing
about digest-only persistence, so one slice could persist plaintext or a different digest scheme.

### F6 — LOW — SQLite-backed SMS limits vs process limiter split not captured

The spine maps rate limiting to `httpserver` with the 50,000-key process limiter implied by
NFR bindings. `docs/database-layout.md` (`sms_delivery_attempts`, "Other rate-limit state")
confirms a deliberate split: SMS cooldown/hourly/daily cost limits are durable SQLite rows that
must survive restart; only the short-window limits use the in-process LRU limiter. A slice
implementing SMS limits in the process limiter would violate a confirmed decision. Worth one
clause in the conventions or the capability map.

### F7 — LOW — AD-12 "random one-way fingerprints" wording invites hash-based implementation

`docs/database-layout.md` (audit invariants) says fingerprints are "random opaque values, **not
hashes or encrypted identifiers**" — one fresh UUID v4 per deleted identity, applied consistently
within the deletion transaction, with no surviving mapping. "One-way" reads like a keyed hash of
the original ID; a slice implementing `sha256(id)` would satisfy the spine and violate the doc.
Say "random opaque (not derived from the original ID)".

### F8 — LOW — Spine fixes response instant precision the repo left to API contracts

The conventions table pins "exactly 6 fractional digits persisted/**returned**".
`docs/database-layout.md` pins six digits only for **persisted** instants (input ≤9); response
formatting is per-route API-contract territory the spine itself defers. Harmless if intended as a
new spine-level decision, but it is invented, not repo-confirmed — mark it `[ADOPTED]`-style or
move the "returned" half to the deferred API contracts.

## Not findings (checked, acceptable)

- AD-3 owner-API-only table access and Tx-passing: a spine invention, but compatible with the
  db-layout single-transaction list; no repo doc contradicts it.
- AD-10 "never auto-retry after ambiguous failure": matches AGENTS.md; `docs/architecture.md`'s
  broader "do not retry automatically after failure" is the stricter superset and both allow
  authorized user retries.
- Retry backoff (1 min/5 min, Retry-After ≤1 h) and summary chunking bounds omitted from AD-5:
  acceptable terseness — single kernel implementation, low divergence risk.
- Startup "dispatches queued work" (tutor responses, requeued jobs) not spelled out in AD-11:
  covered adequately by AD-5's per-system rules once F1 is fixed.
- `go test -race` omitted from the Verification convention row: AGENTS.md makes it conditional;
  minor, optional addition.
