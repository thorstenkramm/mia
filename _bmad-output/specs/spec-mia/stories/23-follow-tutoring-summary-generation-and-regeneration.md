---
title: 'Follow Tutoring Summary Generation and Regeneration'
type: 'feature'
created: '2026-09-14'
status: 'in-review'
review_loop_iteration: 0
followup_review_recommended: false
baseline_revision: '5d5c87ece8ca5418950f9e57f604729743f5d21c'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** A nullable summary currently makes queued generation, active generation, delayed automatic retry, terminal
failure, and ordinary unavailability indistinguishable. Regeneration also returns no authoritative state, forcing browser
clients to probe or inspect generic jobs.

**Approach:** Add an authorized summary-lifecycle projection to tutoring-session reads and summary-generation responses.
Map durable work to domain states with authoritative timing, keep regeneration eligibility independent, and atomically
recheck and enqueue the supervisor-only domain action.

## Boundaries & Constraints

**Always:** Represent queued, generating, automatic-retry-scheduled, generated, supervisor-corrected, terminal-failure,
and unavailable explicitly. Return only domain lifecycle, state-change time, read time, applicable retry time, correction
source/attribution, and server-authored regeneration eligibility. Student owners and currently assigned course supervisors
may read completed-session lifecycle; only assigned supervisors may regenerate. Keep transcript access independent,
existence-hide missing and out-of-scope sessions, preserve corrections against delayed work, atomically queue completion
and regeneration, enforce one active summary job, use strict UTC instants, and set `no-store`.

**Block If:** Implementation requires changing who may view transcripts or summaries, permitting student regeneration,
deleting job history, exposing provider diagnostics, or choosing a new retry/data-retention rule not fixed by FR-63,
FR-64, and FR-84.

**Never:** Infer lifecycle from summary content absence, elapsed client time, generic job-list visibility, or provider
payloads; expose job IDs, attempt counts, failure codes, lease data, usage, or a generic job retry; automatically replay an
ambiguous regeneration; let stale generation overwrite a correction; refresh browser-session idle expiry from reads; or
make summary processing gate retained-transcript access.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Completion | Idle active owned session | Completion and one summary job commit; returned lifecycle is queued | Rollback both on failure |
| Processing | Initial queued or running job | Queued or generating with authoritative state time and checked time | No internals exposed |
| Retry | Failed transient attempt requeued | Automatic-retry-scheduled with authoritative retry instant | No manual action implied |
| Success/correction | Generated result or supervisor edit | Generated or supervisor-corrected with source, attribution, and update time | Late job cannot overwrite |
| Terminal failure | Attempts exhausted or permanent failure | Terminal-failure, no successful-looking content, supervisor regeneration allowed | Transcript remains readable |
| Ineligible regeneration | Any other state or student caller | Eligibility false; no probe needed | Mutation conflicts or existence-hides |
| Concurrent regeneration | Two eligible supervisor requests | Exactly one active job; both reconcile via the same current lifecycle | No automatic replay |
| Scope loss/deletion | Missing session or assignment removed | No lifecycle or job metadata leaks | Uniform not found |

</intent-contract>

## Code Map

- `migrations/000020_job_state_timing.up.sql` -- persist an authoritative job state-transition instant used to represent
  summary lifecycle without elapsed-time inference.
- `internal/jobs/jobs.go` and `internal/jobs/jobs_test.go` -- maintain state timing on claim, retry, recovery, success,
  failure, and cancellation while retaining generic work details inside the jobs owner.
- `internal/tutoring/{types,store,service}.go` -- define and load the authorized domain projection, separate regeneration
  eligibility from lifecycle, return equivalent state from completion/regeneration, and serialize competing enqueues.
- `internal/tutoring/summary.go` -- retain correction-safe guarded success and terminal-failure lifecycle behavior.
- `internal/tutoring/{service,handler}_test.go` -- cover every lifecycle, timing, role/scope, transcript independence,
  correction race, regeneration race, deletion, and one-job invariant.
- `internal/tutoring/handler.go` -- encode lifecycle inside session resources and return current summary lifecycle from the
  regeneration command through shared JSON:API/error/no-store handling.
- `api-doc/{paths/tutoring.yaml,schemas/resources/resources.yaml}` and `docs/{api,tutoring-sessions,product-requirements,
  database-layout,jobs}.md` -- publish domain states, fields, eligibility, reconciliation, errors, timing, and persistence.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000020_job_state_timing.up.sql`, `internal/jobs/jobs.go` -- establish authoritative transition timing
  for durable summary states.
- [x] `internal/tutoring/{types,store,service,summary}.go` -- implement lifecycle projection and atomic one-job
  regeneration while preserving correction and deletion guards.
- [x] `internal/tutoring/{service,handler}_test.go`, `internal/jobs/jobs_test.go` -- prove the complete edge-case matrix,
  including concurrency and authorization boundaries.
- [x] `internal/tutoring/handler.go`, `api-doc/**`, and affected `docs/*.md` -- expose and document the browser contract
  without generic job internals.

**Acceptance Criteria:**

- [x] Given an authorized student owner or assigned supervisor reading a completed session, when summary state is
  returned, then lifecycle is explicit and independent from nullable content and generic job visibility.
- [x] Given completion, transient retry, terminal failure, generated output, or supervisor correction, when state changes,
  then returned domain lifecycle and authorized timestamps are authoritative and transcript access remains independent.
- [x] Given any lifecycle and viewer, when regeneration eligibility is read, then it is true only for an assigned
  supervisor after terminal failure with no summary and no active job, and otherwise false without a mutation probe.
- [x] Given eligible concurrent regeneration requests, when conditions are atomically rechecked, then exactly one job is
  queued and clients can reconcile through equivalent current lifecycle without replay or job metadata.
- [x] Given delayed completion, failure, deletion, or lost assignment, when summary work commits or state is read, then
  corrections and deleted targets are never overwritten or recreated and out-of-scope state remains hidden.
- [x] Given HTTP and OpenAPI conformance checks, when lifecycle reads and regeneration are exercised, then concrete
  JSON:API shapes, strict UTC instants, stable errors, CSRF, and `no-store` match the contract.

## Spec Change Log

## Review Triage Log

Implementation is ready for the supervisor's independent review.

## Design Notes

The lifecycle is a tutoring-domain projection over retained session and job facts, not a new execution system and not a
public job view. A jobs-owned state-transition instant gives every queued/running/retry/terminal transition an
authoritative timestamp. Tutoring maps those facts to safe domain states and exposes neither the job identity nor its
diagnostics. Supervisor correction takes precedence over any retained or in-flight job state because the guarded summary
commit already requires correction-compatible session fields.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: formatting, Go test/vet/lint/race, duplication, vulnerability, OpenAPI, and Markdown
  gates pass; optional unavailable tools are reported by the script.

## Auto Run Result

Status: implemented

Added an explicit tutoring-summary lifecycle to every tutoring-session representation. Student owners and assigned
supervisors can distinguish unavailable, queued, generating, automatic retry, generated, corrected, and terminally
failed state without inspecting nullable content or generic jobs. The projection contains only authoritative state,
state-change, read, retry timing, and viewer-specific regeneration eligibility.

Added durable job state-transition timing and a jobs-owned subject inspection API. Completion returns the atomically
queued lifecycle. Eligible supervisor regeneration now returns `202` with equivalent queued state, serializes concurrent
requests, and never exposes or retries a generic job. Supervisor corrections transactionally mark active summary work
cancelled so delayed lease-guarded results cannot overwrite them or leave durable work stranded.

Updated migrations, jobs and tutoring code/tests, SQLite migration fixtures, OpenAPI, and the API, database, jobs,
tutoring, and product contracts. The supervisor-owned sprint status file was preserved without modification by this run.

Verification: targeted tests, all Go tests, vet, golangci-lint, race tests, pinned Redocly lint, govulncheck, and the final
full project suite passed. The first full-suite run found one local clone; a shared session-view loader fixed it, and the
final duplication gate reported zero clones.

Residual risks: none identified. No independent review was run because the supervisor requested direct execution without
nested agents.
