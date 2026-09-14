---
title: 'Observe and Control Current Tutoring Work'
type: 'feature'
created: '2026-09-14'
status: 'done'
review_loop_iteration: 0
followup_review_recommended: false
baseline_revision: '46a28787350b3e0af99fdc185444dbc8ea3820d4'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Existing transcript, mutation, and SSE surfaces do not provide one authoritative view of a session's
generating and queued work or tell the browser which tutoring actions are currently eligible. Ambiguous mutations and
automatic queue handoff can therefore leave tabs unable to distinguish committed work from stale local state.

**Approach:** Add an owner-scoped, atomic current-work projection with server-authored action eligibility and explicit
reconciliation of known request and response identities. Make submit, retry, interruption, and completion return that
projection or identify the same immediate read, while retaining immutable history and provider-work guarantees.

## Boundaries & Constraints

**Always:** Enforce one generating response and one queued message/response in SQLite transactions. Current work must
separately identify the generating response, queued message and response, remaining queue capacity, session state, and
eligibility for submit, queue, stop, retry, and finish. Eligibility is advisory output; every mutation atomically
rechecks authorization and state. Scope all work and reconciliation identities to the owning student, preserve request-ID
payload idempotency, expose sanitized response states only, set `no-store`, and leave authentication deadlines unchanged.

**Block If:** A required outcome needs a new authorization boundary, automatic provider retry, session end state,
request-ID retention rule, or provider cancellation guarantee not fixed by FR-55, FR-56, and FR-60 through FR-64.

**Never:** Infer work from transcript pagination, manager memory, elapsed time, SSE delivery, or local browser state;
start provider work from reads/reconnects; replay an unsafe request after ambiguity; attribute a newly generating queued
response to the prior terminal response; expose another student's session or work; discard immutable messages or partial
responses; refresh browser-session idle expiry; or let staff finish a student session.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Idle | Active owned session, no queued/generating response | Explicit idle work, one remaining queue slot, submit and finish eligible | No error expected |
| Generating | One generating response, no queued work | Generating response identity, queue eligible, stop target, finish ineligible | No error expected |
| Full | Generating response plus queued message/response | Both immutable pairs, zero capacity, only valid stop targets eligible | Excess work conflicts |
| Handoff | Prior response becomes terminal while queued response starts | Prior and new response identities remain distinct and reconcilable | No duplicate provider request |
| Retry | Failed/interrupted response, active idle session, request ID | One linked retry; identical replay returns it | Changed replay or busy/completed state conflicts |
| Stop | Immutable queued/generating response target | Target outcome and post-stop current work are reconcilable independently | Unknown/out-of-scope is existence-safe |
| Finish | Active session with no queued/generating work | Completion and summary enqueue commit atomically | Busy conflict preserves and exposes current work read |
| Completed/lost | Completed, deleted, inaccessible, or dependency failure | Completed outcome or uniform unavailable result | No messages or retries accepted |

</intent-contract>

## Code Map

- `migrations/000019_tutoring_retry_idempotency.up.sql` -- add retained retry request ID and digest columns plus the
  database uniqueness needed for payload-bound replay.
- `internal/tutoring/types.go` -- define current-work, action-eligibility, and reconciliation projections without
  duplicating persisted message/response types.
- `internal/tutoring/store.go` -- load current slots, terminal/reconciled identities, and retry replays inside the owning
  transaction; SQLite remains authoritative rather than manager memory.
- `internal/tutoring/service.go` -- construct current work atomically; serialize submit/retry/stop/finish against the
  session writer lock; add retry request IDs and ensure mutation outcomes point to authoritative reconciliation.
- `internal/tutoring/manager.go` -- retain immutable response identity across terminal commit and queued dispatch; reads
  and subscriptions must not dispatch duplicate provider work.
- `internal/tutoring/handler.go` -- register owner-only current-work and response reconciliation reads, decode bounded
  retry commands, and encode concrete JSON:API current-work resources through shared protocol helpers.
- `internal/tutoring/{service,handler}_test.go` -- prove every matrix state, two-tab races, handoff identity, request-ID
  replay, stop-target reconciliation, startup recovery, deletion/scope loss, no duplicate calls, and non-refreshing reads.
- `api-doc/{openapi.yaml,paths/tutoring.yaml,schemas/requests/requests.yaml,schemas/resources/resources.yaml}` -- publish
  concrete work states, action objects, reconciliation parameters/results, errors, SSE behavior, security, and no-store.
- `docs/{api,tutoring-sessions,product-requirements}.md` -- explain authoritative work/action reconciliation and preserve
  the PRD behavior contract without duplicating field-level schemas.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000019_tutoring_retry_idempotency.up.sql`, `internal/tutoring/{types,store,service}.go` -- implement
  durable retry idempotency and one-snapshot current work with independently calculated action eligibility.
- [x] `internal/tutoring/{service,manager}_test.go` -- harden queue, handoff, stop, retry, completion, recovery, deletion,
  and concurrent-tab invariants, including an exact provider-call count.
- [x] `internal/tutoring/{handler,handler_test}.go` -- expose owner-scoped current work and mutation reconciliation using
  shared JSON:API, authentication, CSRF, error, and no-store behavior.
- [x] `api-doc/**`, `docs/{api,tutoring-sessions,product-requirements}.md` -- document every state, eligibility field,
  mutation/reconciliation path, SSE reconnect rule, stable error, and non-refreshing behavior.

**Acceptance Criteria:**

- [x] Given any active current-work state, when the owner reads it, then one atomic response identifies idle,
  generating, queued message/response, remaining capacity, immutable IDs/states, and independent submit, queue, stop,
  retry, and finish eligibility without transcript inference.
- [x] Given bounded submit/queue or payload-bound retry commands, when requests race or replay, then one generating and
  one queued maximum hold, identical request IDs return existing work, changed payloads conflict, and definitive output
  provides equivalent current work or the immediate authoritative reconciliation read.
- [x] Given automatic terminal-to-queued handoff or Stop targeting an immutable response, when the browser reconciles,
  then the target's terminal result and the newly current work retain distinct IDs and cannot be conflated.
- [x] Given completion, reconnect, restart, scope loss, deletion, or provider/dependency failure, when reads and actions
  race, then only an idle active owner can finish or retry, completed work rejects mutation, no read/SSE starts duplicate
  provider work, and missing/out-of-scope results remain indistinguishable.
- [x] Given HTTP and OpenAPI conformance tests, when all current-work and mutation surfaces are exercised, then concrete
  JSON:API/SSE shapes, CSRF, errors, no-store, bounds, and unchanged authentication idle deadlines match the contract.

## Spec Change Log

## Review Triage Log

### 2026-09-14 — Review findings fix

- patch: 2 (high 0, medium 2, low 0)
- addressed_findings:
  - `[medium]` `[patch]` Submit, retry, and completion now reference endpoint-specific typed OpenAPI documents whose
    required `links.current_work` schemas describe their message-request, response-target, or query-free reconciliation.
  - `[medium]` `[patch]` A repeated deterministic manager-backed Stop test now proves a generating target becomes
    interrupted while queued work hands off under a distinct immutable response ID in returned and refreshed work.

### 2026-09-14 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 0
- defer: 0
- reject: 0
- addressed_findings:
  - none

## Design Notes

Current work is a session-owned projection, not another execution system or event log. It may carry explicit
reconciliation results for caller-supplied retained request/response identities, but those results are loaded in the same
transaction and cannot broaden scope. A terminal target and the current generating response are separate fields so queue
handoff never reuses or obscures identity.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: no formatting differences.
- `./run-all-tests.sh` -- expected: formatting, Go test/vet/lint/race, duplication, vulnerability, OpenAPI, and Markdown
  gates pass; optional unavailable tools are reported by the script.

## Auto Run Result

Status: done

Implemented an owner-scoped current-work read that atomically exposes persisted generating and queued work, remaining
capacity, immutable reconciliation targets, and independent eligibility for submit, queue, Stop, retry, reconnect, and
finish. Optional retained message request and response IDs resolve ambiguous outcomes without replaying unsafe work.

Added payload-bound retry request idempotency, including replay after completion, and changed successful Stop to return
the stopped response outcome together with authoritative current work. Mutation responses identify the immediate
current-work read; reads and SSE reconnects remain non-refreshing and never dispatch provider work.

Changed the tutoring migration, feature types/store/service/handlers and tests, SQLite migration fixtures, OpenAPI
paths and concrete schemas, and tutoring API, persistence, design, and product contracts. The supervisor-owned sprint
status file was preserved without modification by this run.

Review findings: no remaining patch, deferred, or rejected findings. Follow-up review recommendation: false (0 high,
0 medium, 0 low patches; score 0).

Verification: targeted tutoring and SQLite tests passed. Pinned Redocly lint passed. Final `./run-all-tests.sh` passed
gofmt, all Go tests, vet, golangci-lint, race tests, duplication checks, Trivy, Redocly, and Markdown lint.

Residual risks: none identified.

Review fix: submit responses now carry the canonical message request ID in their `current_work` link; endpoint-specific
OpenAPI wrappers type that link for submit/replay, retry/replay, and completion. Added a manager-backed Stop/handoff test
that passed 20 consecutive runs and remains covered by the full race-enabled project suite.
