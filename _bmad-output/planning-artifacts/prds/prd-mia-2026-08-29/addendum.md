# PRD Addendum — MIA

Technical depth extracted from the repository that belongs downstream (architecture, API design, implementation)
rather than in the PRD narrative. All items are confirmed repository decisions unless marked open.

## Technology stack (confirmed)

- Go, module `github.com/thorstenkramm/mia`, minimum Go 1.27.0; single dependency-free x86-64 Linux binary.
- Echo 5.3.1 web framework; Gorilla CookieStore sessions signed+encrypted with `session.key` (64 random bytes,
  generated on first start, part of the backup set; losing it invalidates every cookie).
- SQLite via CGo-free `modernc.org/sqlite`; fixed path `data_dir/mia.sqlite3`; WAL, `synchronous=FULL`, foreign
  keys, 5 s busy timeout; pool of 4 open / 4 idle connections, no lifetime expiry.
- Embedded `golang-migrate` v4 forward-only migrations, run automatically before database use; dirty or
  newer-than-executable schema refuses to start.
- Exclusive non-blocking OS lock on `data_dir/mia.lock` acquired before DB open by every database-using command.
- Embedded tzdata; `x/text` for BCP 47; tiktoken-go `o200k_base` tokenizer; embedded SecLists top-100k password
  list (updated only via releases).
- Config precedence: defaults < TOML < environment < flags; mechanical name derivation; secrets via TOML/env only
  (no flags); unknown keys are startup errors; strict config-file ownership/permission checks.
- Operator defaults: config file `/etc/mia/mia.toml`; listener `127.0.0.1:9900` (TCP or Unix socket); logging to
  stderr at `info` in `json` format; SMTP port 587 with `starttls`; `main.public_url` is the mandatory HTTPS
  origin for emailed links (never derived from request headers).

## Data directory layout

`mia.sqlite3`, `mia.lock`, `session.key`, `llm-instructions/` (operator-editable prompt files: an init prompt
plus per-job and per-material-type prompts; MIA creates them but never overwrites them; operator edits require a
server restart), `courses/<id>/logo.png`,
`materials/<id>/files/<file-id>/{file,content.jsonl}`, `users/<id>/avatar.png`, `tts-cache/<speech-id>.mp3`.
Avatars/logos are filesystem-only; presence at the fixed path determines availability. Data directory permissions:
0700 directories / 0600 files; the whole directory plus SQLite is one consistent backup unit.

## API conventions (draft, from docs/api.md — not authoritative)

- All routes under `/api/v1`; JSON:API media type for resources and errors; snake_case attributes.
- Offset pagination (`page[limit]` default 25 / max 100, offset ≤10,000); no exact totals by default.
- Cookie `__Host-mia_session` (Secure, HttpOnly, SameSite=Lax) holds only user ID, stage, challenge ID, security
  generation, and timers; all account state reloaded per request. CSRF via Echo middleware + Fetch Metadata +
  `__Host-mia_csrf` token in `X-CSRF-Token`; token rotates on login completion, logout, and stage transitions.
  Same-origin only; no CORS.
- SSE tutor stream (`GET /tutor-responses/{id}/events`): `snapshot` on connect, then `started`/`delta` and a
  terminal `completed`/`interrupted`/`failed`; 15 s heartbeat comments; 30 s write deadline; per-subscriber queue
  bounded at 64 events / 256 KiB (overflow closes only that connection); reverse proxy must disable buffering.
- Bearer tokens travel in request bodies or URL fragments, never query strings, to stay out of access logs.

## Job system mechanics

- Single `jobs` table as queue and state record; first version runs one worker, one job at a time, 1 s idle poll.
- Atomic claim with 2-minute lease and unique lease token, renewed every 30 s; result commits require the current
  lease token (guarded zero-row commits discard stale output and delete newly published files).
- ≤3 attempts; transient failures retry after 1 min then 5 min; valid provider `Retry-After` may extend delay up
  to 1 h; permanent failures (validation, authorization, malformed provider output) never retry.
- Job types: per-file OCR extraction (Mistral for PDF/PNG/JPEG; bounded local parsers for DOCX/text/Markdown),
  material summary (queued by the last successful extraction in the same transaction), tutoring-session summary
  (queued atomically at completion). Tutor-response generation and speech generation are NOT jobs-table jobs —
  they use their own in-memory queue/cache lifecycles with the stranded-failure startup rule.
- Startup: expired running leases requeue if attempts remain, else the subject's terminal-failure transition
  applies.
  Graceful shutdown: stop claiming, ≤30 s grace, cancel and requeue if attempts remain.
- Summarization pipeline (materials and sessions): canonical ordering, ≤24,000-token chunks, ≤64 chunks,
  ≤2 greedy reduction rounds, 2,048-token output per call; coverage verified before the first provider call;
  intermediate summaries in memory only; usage counters cumulative and unattributed.

## Provider deadlines (fixed) and default models

- OpenAI streaming (tutor): 30 s to first event, 60 s between events, 10 min total.
- OpenAI summaries: 10 s response headers, 2 min total. Mistral OCR chunk: 30 s headers, 5 min total.
- SMTP: 10 s connect/TLS/command, 30 s total. ClickSend: 5 s connect, 15 s total. ElevenLabs: 10 s headers,
  2 min total.
- HTTP server: 5 s headers, 30 s non-upload body, 15 min upload body, 120 s idle, 30 s graceful shutdown.
- Default models: `gpt-5.6-terra` for chat and jobs (configurable; must support Responses API and, for chat,
  function calling); OCR fixed to `mistral-ocr-4-1`.

## Database design highlights

- Prefixed UUID v4 text primary keys (`u_`, `cou_`, `mat_`, `ts_`, `job_`, `aud_`, …); instants as RFC 3339 UTC
  with 6 fractional digits; enums via CHECK constraints; validated JSON columns.
- Normalized-key uniqueness: ASCII lowercase for username/email; NFC + Unicode case fold for course/material
  names (no SQLite `NOCASE`).
- `users.security_generation` implements mass cookie invalidation (temporary password, student MFA reset).
- One-active-tutoring-session-per-student enforced by a partial unique index on `tutoring_sessions`.
- Attribution foreign keys `SET NULL` on actor deletion; ownership foreign keys cascade; audit de-identification
  via random one-way fingerprints.
- Durable SMS limiter state in `sms_delivery_attempts` (survives restarts, unlike the in-process LRU limiter).

## Cross-document tensions for the architecture phase

Recorded during extraction; resolve before or during architecture:

1. Tutor-response generation and speech generation sit outside the jobs-table machinery (no lease/attempt
   semantics); whether they appear in `GET /jobs` is unspecified.
2. Shutdown asymmetry is intentional but implicit: interactive tutor generation is cancelled-and-failed after
   30 s, while background jobs are cancelled-and-requeued when attempts remain.
3. `GET /tutoring-sessions/{id}/materials` is listed without semantics (used vs selected; per-role visibility).
4. The SSE response-state enum shown in examples lacks an explicit `queued` value; snapshot behavior for a
   subscriber attached to a never-started queued response is unspecified.
5. `GET /users` must express three very different visibility shapes (admin metadata, supervisor course scope,
   mentor minimal identity) on one route — flagged as an authorization-design risk. The mentor minimal-identity
   field set is decided (username, name, nickname, avatar; PRD §13 resolutions); the route shaping remains an
   API-contract question.
6. Domain-level retries (material re-finalization, summary regeneration) must be presented as domain actions,
   not job operations, to stay consistent with "no generic job retry".

## Open implementation-time items (confirmed deferred)

- Temp-file placement and fsync/directory-sync strategy: specified with implementation.
- Prompt bodies for tutor/brief/summary instructions: written and reviewed with each feature.
- Provider adapter contracts (fields, streaming events, retryable error classes): documented per provider at
  implementation time.
- SecLists blocklist upstream version/license: recorded when the list is added.
- Automated check keeping `mia.example.toml` mechanically complete: required once config code exists.
