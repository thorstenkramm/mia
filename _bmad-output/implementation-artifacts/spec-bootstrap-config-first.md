---
title: 'Validate Bootstrap Configuration First'
type: 'bugfix'
created: '2026-09-05'
status: 'done'
route: 'one-shot'
---

# Validate Bootstrap Configuration First

## Intent

**Problem:** Interactive `bootstrap-admin` asked for account and password input before determining the configured
database location, so an absent or invalid `main.data_dir` was reported only after the dialogue.

**Approach:** Validate the offline configuration before selecting either input mode or opening a password file, while
leaving the existing lock-before-SQLite and post-commit behavior unchanged.

## Suggested Review Order

**Configuration ordering**

- Validates the data directory before prompts or password-file input can begin.
  [`main.go:60`](../../../cmd/mia/main.go#L60)

**Regression coverage**

- Proves missing and invalid directories beat an unreadable password-file error.
  [`main_test.go:339`](../../../cmd/mia/main_test.go#L339)
