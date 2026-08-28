# Architecture

## Purpose

This document records confirmed programming and architecture decisions. Product
behavior remains defined by the
[product requirements](product-requirements.md).

## Go Baseline

- The Go module path is `github.com/thorstenkramm/mia`.
- The minimum supported Go version is `1.27.0`.

The package layout remains open and will be introduced incrementally as concrete
implementation responsibilities require it.

## Executable

MIA ships one executable named `mia`. It provides these subcommands:

- `mia serve` starts the HTTP server and background workers.
- `mia bootstrap-admin` creates the first administrator through the confirmed
  offline interactive workflow.
- `mia reset-admin-mfa` performs the confirmed offline sole-administrator MFA
  recovery workflow.

The executable contains one shared implementation of configuration, persistence,
account policy, and auditing. Local commands do not require a separate
administration binary.

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

These settings are fixed for the MVP and are not operator-configurable.

## Identity Representation

- MIA uses `golang.org/x/text/language` to parse and canonicalize BCP 47 language
  tags.
- MIA embeds Go's time-zone database through `time/tzdata` so validation and
  generated communications do not depend on host time-zone files.
- Password length counts Unicode code points without trimming or Unicode
  normalization. MIA rejects invalid UTF-8 before hashing.

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
  `content.txt` files. The MVP stores no separate search index and no
  provider-managed material files.
