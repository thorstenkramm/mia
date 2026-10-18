---
title: 'Align CI linter with Go 1.27.2'
type: 'chore'
created: '2026-10-18'
status: 'done'
route: 'one-shot'
---

# Align CI linter with Go 1.27.2

## Intent

**Problem:** GitHub Actions run 39123252646 failed because golangci-lint v2.13.2 could not decode Go 1.27.2 export
data version 5. Local verification used compatible v2.15.0, hiding the CI tooling mismatch.

**Approach:** Pin CI's linter installation to v2.15.0. The full local `./run-all-tests.sh` passes with Go 1.27.2 and
golangci-lint v2.15.0, including race tests, vulnerability scans, duplication, API lint and Markdown lint. The first
attempt exceeded the command timeout during race tests; a complete rerun passed. CI execution awaits a push.
Review found one pre-existing inconsistency in agent-rule linter versions, recorded in deferred work; no patch
findings remain.

## Suggested Review Order

- Use the compatible linter version already verified locally.
  [`tests.yml:26`](../../.github/workflows/tests.yml#L26)
