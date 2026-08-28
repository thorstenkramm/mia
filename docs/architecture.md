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
