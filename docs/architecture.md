# Architecture

## Purpose

This document records confirmed programming and architecture decisions. Product
behavior remains defined by the
[product requirements](product-requirements.md).

## Go Baseline

- The Go module path is `github.com/thorstenkramm/mia`.
- The minimum supported Go version is `1.27.0`.

Initial scaffolding is limited to command wiring, configuration, process locking,
SQLite and migrations, identity validation, the HTTP server, and the first
authentication slice. Feature packages are added only as they are implemented;
MIA does not pre-create a speculative domain tree.

## Executable

MIA ships one executable named `mia`. It provides these subcommands:

- `mia serve` starts the HTTP server and background workers.
- `mia bootstrap-admin` creates the first administrator through the confirmed
  offline workflow.
- `mia reset-admin-mfa` performs the confirmed offline sole-administrator MFA
  recovery workflow.

The executable contains one shared implementation of configuration, persistence,
account policy, and auditing. Local commands do not require a separate
administration binary.

### Process lock and local commands

- Every command that opens SQLite first acquires a non-blocking exclusive OS lock
  on `main.data_dir/mia.lock`. `mia serve` holds it for the process lifetime.
  Another server or an offline command refuses to run while the lock is held.
- The mode-`0600` lock file may remain on disk. Process exit releases the OS lock;
  MIA does not infer ownership from file existence or delete a stale file.
- Offline commands use normal configuration precedence and reject malformed or
  unknown configuration keys, but semantically validate only `main.data_dir` and
  settings required for database and account-policy work. They do not require a
  document root or provider configuration.
- The process lock is acquired before opening SQLite or applying migrations.
- `bootstrap-admin` uses either an interactive terminal dialogue for username,
  email, language, country, time zone, password, and confirmation, or a complete
  noninteractive set of account flags plus `--password-file`. The two modes are
  exclusive. The password file is a readable regular file containing one line
  with an optional final LF or CRLF; no password bytes are otherwise changed.
- Interactive password input provides asterisk feedback while preserving entered
  bytes, retries mismatch and policy failures, and restores terminal state on
  every exit. The command prints its fixed SQLite path only after commit.
- `reset-admin-mfa` displays the sole administrator and requires the operator to
  type its exact displayed username before making changes.

## SQLite

- MIA uses the CGo-free `modernc.org/sqlite` `database/sql` driver.
- The database is the fixed `mia.sqlite3` file in `main.data_dir`.
- MIA uses `golang-migrate` v4 as a library. Numbered SQL migrations are embedded
  in the executable through `io/fs`; no external migration files or executable
  are required.
- `mia serve` and every local command that opens the database apply pending up
  migrations before other work. They refuse a dirty schema or a schema newer
  than the executable. MIA does not run down migrations.
- Every connection enables foreign-key enforcement, a five-second busy timeout,
  WAL journal mode, and `synchronous=FULL`.
- The pool has four open and four idle connections with no connection-lifetime
  expiry. MIA does not use separate reader and writer pools.

These settings are fixed for the MVP and are not operator-configurable.

## Identity Representation

- MIA uses `golang.org/x/text/language` to parse and canonicalize BCP 47 language
  tags.
- MIA embeds Go's time-zone database through `time/tzdata` so validation and
  generated communications do not depend on host time-zone files.
- Password length counts Unicode code points without trimming or Unicode
  normalization. MIA rejects invalid UTF-8 before hashing.
- MIA embeds the versioned SecLists top-100,000 common-password list in the
  executable. The repository records the upstream version and license when the
  list is added, and MIA updates it only through normal releases.
- Blocklist comparison uses the submitted valid UTF-8 bytes exactly. MIA does not
  trim, normalize, case-fold, or generate password mutations.
- Course and material display names are trimmed and NFC-normalized. Their stored
  unique keys use `cases.Fold` and a final NFC normalization; SQLite `NOCASE` is
  not used.

## HTTP Runtime

- Non-upload request bodies have a one MiB hard limit; individual routes may use
  lower semantic limits.
- The server allows five seconds for request headers, 30 seconds for a non-upload
  body, 15 minutes for an upload body, and 120 seconds for an idle HTTP
  connection.
- Each SSE write has a 30-second deadline. An idle tutor-response stream sends a
  comment heartbeat every 15 seconds.
- Graceful shutdown allows 30 seconds before canceling remaining request and
  streaming work.
- Static GET and HEAD serving permits regular files only, rejects symlink escapes,
  dotfiles, and directory listings, and applies SPA fallback only outside `/api`.
  Responses use `nosniff`, restrictive referrer and framing policies, and a
  strict baseline CSP (`default-src 'self'; object-src 'none'; base-uri 'self';
  frame-ancestors 'none'`), loosened during frontend integration only when the
  frontend demonstrably requires it.

### Client addresses

- MIA recognizes only `X-Forwarded-For`. It ignores `Forwarded`, `X-Real-IP`, and
  every other client-address header.
- A TCP peer must belong to a configured trusted proxy CIDR or a loopback network
  before MIA considers the header. Loopback networks are always trusted.
- MIA parses at most 20 comma-separated plain IP addresses and at most two KiB of
  header data. Ports, zone identifiers, empty elements, and malformed addresses
  make the complete header invalid.
- Starting at the direct peer, MIA walks the chain right-to-left across trusted
  proxy addresses. The first untrusted address is the client. If every forwarded
  address is trusted, the leftmost address is the client.
- An absent, malformed, or excessive trusted-proxy header falls back to the
  direct TCP peer. IPv4-mapped IPv6 addresses normalize to IPv4.
- A permission-controlled Unix listener is a trusted proxy transport. It uses the
  same bounded header parser and one shared local limiter key when the header is
  absent or invalid; audit `source_ip` is then null.

## Tutor Context And Retrieval

- One student message contains at most 8,000 Unicode code points and 32 KiB of
  valid UTF-8.
- One tutor request uses at most 32,000 model-input tokens and permits at most
  2,048 generated output tokens.
- MIA counts input with a local tokenizer matching the configured OpenAI model.
  An unsupported model-tokenizer mapping is a startup configuration error.
- Required instructions and the current student message take priority. MIA then
  includes the newest complete conversation turns that fit and omits older turns
  without generating or persisting a rolling summary.
- Complete selected-material content is included only when it contains at most
  8,000 Unicode code points and 32 KiB and fits the remaining token budget.
- One tutor response may perform at most three local retrieval rounds and use at
  most eight excerpts. Each excerpt contains at most 4,000 Unicode code points
  and 16 KiB and remains subject to the total input budget.
- Retrieval performs bounded streaming searches over authorized normalized
  `content.jsonl` files. Search uses NFC-normalized Unicode-folded terms and
  deterministic match-count, frequency, material, file, and segment ordering.
  One adjacent segment per side may be included within excerpt limits, and
  overlapping excerpts are deduplicated. The MVP stores no separate search index and no
  provider-managed material files.
- Retrievable content must be ready and file-backed in the session's course.
  Course-wide material also requires approval; student-private material requires
  ownership by the active student and no approval. Link-only metadata has no
  retrievable source content.

## Provider Operations

- Streamed OpenAI tutoring waits at most 30 seconds for the first event, 60
  seconds between events, and ten minutes overall.
- Non-streaming OpenAI summary requests use a ten-second response-header timeout
  and a two-minute total deadline.
- Each bounded Mistral OCR chunk uses a 30-second response-header timeout and a
  five-minute total deadline.
- Background retries use current server configuration and processing code. MIA
  does not guarantee identical provider requests or provider-side idempotency
  across retries, restarts, configuration changes, or upgrades. Lease-guarded
  commits prevent duplicate durable output, but ambiguous retries may repeat paid
  provider work.
- SMTP delivery uses ten-second connect, TLS, and command deadlines within a
  30-second total operation.
- A definite initial invitation-delivery failure creates a faulty invitation. An
  SMTP timeout is ambiguous operational success: the invitation remains pending,
  MIA logs a sanitized error, and it does not retry automatically. Email is
  English-only plain-text UTF-8 with sanitized headers and no HTML part.
- ClickSend uses a five-second connection timeout and a 15-second total deadline.
- ClickSend's configurable base URL defaults to `https://rest.clicksend.com/v3`.
  It accepts only root origins or `/v3` base paths, each with an optional terminal
  slash, and the adapter composes only `POST /sms/send`. HTTP is permitted only
  for `localhost` or a loopback-IP compatible local test double.
- ElevenLabs uses a ten-second response-header timeout and a two-minute total
  deadline.
- Request-path provider operations do not retry automatically after failure.
  Authorized user retries and resends remain subject to normal state and rate
  limits. Background jobs use their separate durable retry policy.
- Startup validates provider configuration and supported model-tokenizer mappings
  locally but makes no provider call. Provider outages do not prevent MIA from
  starting.
- Provider adapters allowlist supported request, response, stream, and usage
  fields. Missing or malformed required structures fail safely; unknown optional
  fields are ignored. Raw payloads are neither retained nor exposed, and only
  documented provider errors are classified as retryable.
- Mistral OCR uses fixed model `mistral-ocr-4-1`. Token counting uses
  `github.com/tiktoken-go/tokenizer` with `o200k_base` for the confirmed OpenAI
  models.

## Instruction Defaults

Short tutor, material-brief, and session-summary defaults are embedded in the
executable. Startup creates only missing instruction files and never overwrites
operator files. There is no runtime versioning or prompt migration; Git and
release history track the embedded source.

The exact prompt body for each feature is written and reviewed with that
feature's implementation. Prompts guide model behavior; Go code remains
responsible for authorization, input bounds, output-schema validation, and
security invariants.

## Implementation Prerequisites

Before implementing a provider adapter, its current API contract must define the
exact outbound fields, required inbound fields, supported streaming events,
normalized usage values, and retryable error classes. This documentation is
written against the provider API used by that implementation rather than frozen
prematurely during product planning.

Before implementing an affected HTTP route, its API contract must define exact
attributes, writable fields, relationships, filters, ordering, status codes,
stable errors, authorization, redaction, idempotency, and field-level bounds. The
authentication and session family is the first complete vertical slice.

## Startup Storage Integrity

Validated source uploads are atomically renamed to their deterministic path
before the database row commits. Transaction failure removes the published file,
and startup reconciliation removes source files without rows. Exact
same-filesystem temporary placement and file and directory synchronization are
specified with implementation.

Startup first deletes expired generated-speech rows and files. It then changes
speech rows stranded in generating state to failed with a sanitized restart code
and removes associated incomplete output without repeating provider requests.
Finally, it logs an error and exits when SQLite references a missing source file,
processed `content.jsonl`, or unexpired available generated-speech file. Missing
avatars and course logos are normal because their fixed-path presence defines
availability.
