---
title: 'Bootstrap Administrator Usability'
type: 'feature'
created: '2026-09-05'
status: 'done'
baseline_commit: 'ab3b50ca570e088dbc9a1fadd52b61bb871ddf42'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/docs/product-requirements.md'
  - '{project-root}/docs/architecture.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** `mia bootstrap-admin` currently exits after an opaque `invalid password` error, gives no terminal input
feedback, supports only a terminal workflow, and does not confirm where the one-time administrator was persisted.
Operators cannot reliably automate a safe first-administrator bootstrap.

**Approach:** Make bootstrap support either an improved interactive terminal dialogue or a complete flag and
single-line password-file invocation. Preserve local-only, one-time, transactional bootstrap behavior and make all
configuration, password-policy, and database-path outcomes explicit.

## Boundaries & Constraints

**Always:** Keep bootstrap local-only, unavailable after any administrator exists, and protected by the existing data
directory lock and transaction. Require configured absolute `main.data_dir`; do not create or infer a default data
directory. Noninteractive mode requires `--username`, `--email`, `--language`, `--country`, `--time-zone`, and
`--password-file`; reject partial or mixed modes. Preserve password bytes exactly: the password file permits one line
with an optional final LF or CRLF only; reject a second line, including an empty one. Never log, audit, print, trim,
normalize, or otherwise expose passwords. Print success only after commit as `User <username> has been inserted into
<absolute-data-dir>/mia.sqlite3`.

**Ask First:** Any password-file ownership, mode, symlink, or filesystem-security policy beyond rejecting unreadable,
non-regular input; any change to the approved password policy or account fields; any public route or environment-based
account credentials.

**Never:** Accept `admin@localhost.de` as a username; the approved noninteractive form is `--username admin --email
admin@localhost.de`. Do not replace Argon2id, weaken the common-password check, expose a public bootstrap route, or
invent server-side session state. Do not make the command succeed when `main.data_dir` is absent or ambiguous.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Interactive success | TTY, valid fields, matching policy-compliant password | Asterisks provide keystroke feedback; one administrator commits; full fixed SQLite path prints | Restore terminal state on all exits |
| Interactive policy failure | Password is short, too long, invalid UTF-8, or common | Explain the exact policy failure without echoing the password; prompt for password and confirmation again | Keep prior identity fields; do not open/write SQLite before valid input |
| Interactive mismatch | Password and confirmation differ | Explain mismatch and retry both password prompts | No password content output |
| Noninteractive success | Complete flags plus regular one-line password file | Create the same administrator and print the same post-commit confirmation | File bytes other than one terminal LF/CRLF are preserved |
| Password file invalid | Empty/second line/non-regular/unreadable file | Reject before database write | Do not reveal password content |
| Ambiguous invocation | Missing `main.data_dir`, incomplete flags, flags mixed with TTY mode | Fail with clear configuration or mode error | Never infer a database location |

</frozen-after-approval>

## Code Map

- `cmd/mia/main.go` -- owns Cobra command registration, terminal prompts, bootstrap transaction, and post-commit output.
  Extract input-mode selection and password-file parsing into testable helpers; retain lock/open/transaction ordering.
- `cmd/mia/main_test.go` -- existing bootstrap precondition and terminal tests; add noninteractive, retry, validation,
  path-confirmation, and mode-selection coverage without placing secrets in assertions.
- `internal/identity/identity.go` and `internal/identity/password_test.go` -- password hashing currently yields only
  `ErrInvalidPassword`; expose non-secret policy reasons reusable by terminal feedback while preserving exact bytes.
- `internal/config/config.go` and tests -- offline `Load(..., false)` already validates `main.data_dir`; make an absent
  directory diagnostic explicit rather than an ambiguous path error.
- `internal/sqlite/sqlite.go` and tests -- owns fixed `mia.sqlite3` construction; export a small path helper so success
  output and actual database storage cannot diverge.
- `docs/product-requirements.md`, `docs/architecture.md`, `docs/api.md`, `README.md`, and
  `docs/server-configuration.md` -- update interactive-only statements and explain both supported local modes,
  required data-directory configuration, password-file semantics, and confirmation behavior.

## Tasks & Acceptance

**Execution:**
- [x] `internal/identity/identity.go`, `internal/identity/password_test.go` -- expose precise, non-secret password
  policy failure reasons and test each boundary without changing validation or hashing.
- [x] `internal/config/config.go`, `internal/config/config_test.go`, `internal/sqlite/sqlite.go`,
  `internal/sqlite/sqlite_test.go` -- make missing data-dir errors explicit and centralize the fixed database path.
- [x] `cmd/mia/main.go`, `cmd/mia/main_test.go` -- add exclusive interactive/noninteractive bootstrap modes, secure
  bounded password-file reading, terminal asterisk feedback and retry loops, and post-commit confirmation.
- [x] `README.md`, `docs/product-requirements.md`, `docs/architecture.md`, `docs/api.md`,
  `docs/server-configuration.md` -- make human-readable contracts match the new local bootstrap modes.

**Acceptance Criteria:**
- Given no configured `main.data_dir`, when either bootstrap mode runs, then it fails before lock/database creation with
  a diagnostic stating that `main.data_dir` must be configured.
- Given valid complete noninteractive flags and a one-line password file, when no administrator exists, then exactly
  one verified administrator is created atomically and success prints the username and absolute `mia.sqlite3` path.
- Given a password file with terminal LF/CRLF only, when it is read, then the line ending is removed and all other
  whitespace is retained; given any second line, it is rejected with no database write.
- Given interactive invalid or mismatched passwords, when the operator corrects them, then the dialogue retries until
  valid input or terminal error and never prints password content.

## Design Notes

Password feedback is not permission to alter password bytes. The terminal reader must track input for rendering only,
handle backspace and UTF-8 safely, and restore terminal state with `defer`. If portable masked terminal handling cannot
meet that boundary without a risky dependency, halt under Ask First rather than silently omitting the requested
feedback.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: no formatting diff.
- `go test ./cmd/mia ./internal/identity ./internal/config ./internal/sqlite` -- expected: mode, file, policy, and
  persistence tests pass.
- `./run-all-tests.sh` -- expected: all repository gates pass without modifying the worktree.
- `git diff --check` -- expected: no whitespace errors.

## Suggested Review Order

**Bootstrap modes and persistence**

- Selects exclusive terminal or flag-file input before offline database setup.
  [`main.go:60`](../../../cmd/mia/main.go#L60)

- Retries policy and confirmation failures without exposing submitted password bytes.
  [`main.go:223`](../../../cmd/mia/main.go#L223)

- Reads bounded, regular one-line password files while preserving meaningful whitespace.
  [`main.go:323`](../../../cmd/mia/main.go#L323)

- Restores raw terminal state while rendering UTF-8-aware asterisk feedback.
  [`main.go:387`](../../../cmd/mia/main.go#L387)

**Shared validation and storage contracts**

- Separates non-secret password-policy reasons from Argon2id hashing.
  [`identity.go:84`](../../../internal/identity/identity.go#L84)

- Centralizes the fixed SQLite file path used by storage and confirmation output.
  [`sqlite.go:60`](../../../internal/sqlite/sqlite.go#L60)

**Regression coverage and operator contracts**

- Exercises password-file edges, retries, terminal masking logic, and post-commit bootstrap.
  [`main_test.go:166`](../../../cmd/mia/main_test.go#L166)

- Documents the supported local modes and explicit data-directory requirement.
  [`architecture.md:45`](../../../docs/architecture.md#L45)
