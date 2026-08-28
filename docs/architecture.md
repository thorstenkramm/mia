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
