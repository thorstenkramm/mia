---
title: 'Tutoring sessions and streaming tutor'
type: 'feature'
created: '2026-09-05'
status: 'done'
baseline_commit: '9a58533cdc2ee3a58a1ea32194d3003cf027cf59'
review_loop_iteration: 0
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** MIA has no tutoring-session persistence, grounded interactive tutor, resumable stream, or completed-session
review. Course and material lifecycle gates therefore cannot enforce active-session or selected-material invariants.

**Approach:** Add the `tutoring` vertical slice, streaming OpenAI adapter, bounded material retrieval API, in-memory
response manager, durable summary handler, lifecycle wiring, and concrete authenticated JSON:API/SSE routes.

## Boundaries & Constraints

**Always:** Enforce FR-54..64, NFR-1..3, NFR-12, NFR-16..18, and AD-3..7, AD-10, AD-12, AD-14. One active session per
student is a database invariant across courses/devices. Creation and message request IDs are canonical lowercase UUID v4
values paired with canonical-body SHA-256 digests. Authorize every session, selection, search, excerpt, stream, review,
and mutation by owner or assigned-supervisor scope and state. Keep provider generation independent of browser lifetime;
persist after 16 KiB or one second and before terminal commit. Cap requests at 32,000 input and 2,048 output tokens,
retrieval at three rounds/eight excerpts, and each excerpt at 4,000 code points/16 KiB. Queue session summary atomically
at completion and preserve human corrections. Startup fails stranded generation and resumes queued work; shutdown starts
no queued work and gives generation 30 seconds before cancel-and-fail.

**Ask First:** Any product decision not fixed by the PRD, concrete API draft, or architecture spine, especially a new
authorization, retention, provider retry, or lifecycle rule.

**Never:** Trust model tool calls as authorization, expose raw provider events/payloads/prompts, retry ambiguous tutor
calls automatically, cancel generation on SSE disconnect, let staff force completion, reveal active chat content to
supervisors, inline all material, use a hidden rolling summary, overwrite corrected summaries, or route tutor responses
through the jobs table.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Start/resume | Joined student, active ready course, valid selections/request ID | One active session; identical replay returns it | Second active or changed replay conflicts; invalid selection hides existence |
| Message/work | Active session and bounded message | One generating plus one queued response; durable stream survives disconnect | Third slot conflicts; partial provider failure is retained without retry |
| Retrieval | Model search/excerpt request | Deterministic local authorized excerpts within all budgets; used material recorded | Reject revoked, non-ready, cross-course, or other-owner material |
| Completion/review | Idle owner or assigned supervisor after completion | Summary job queued; completed chat visible; correction attributed and audited | Staff cannot finish; active chat remains hidden; corrected summary is immutable to jobs |
| Recovery | Restart or shutdown during work | Queued resumes; uncertain generation safely fails | Never reissue uncertain provider request |

</frozen-after-approval>

## Code Map

- `migrations/000013_tutoring.up.sql` -- tutoring sessions, selected/used materials, ordered messages/responses,
  request digests, partial unique active-session and queue/generation guards.
- `internal/tutoring/` -- owns scoped session lifecycle, request-ID replay, context budgets, retrieval, manager/SSE,
  summary jobs, lifecycle deletion, resources, and tests.
- `internal/provider/openai/openai.go` -- extend allowlisted Responses API adapter with fixed-deadline streaming and
  internal tool events while retaining structured-job support.
- `internal/material/` -- export only transaction-aware tutoring retrieval/selection APIs; content files remain owned here.
- `internal/course/course.go` -- wire tutoring active-session checks already injected by story 7.
- `internal/jobs/jobs.go` -- reuse durable session-summary queue/leases; no tutor-response jobs.
- `internal/httpserver/{httpserver,errors}.go`, `internal/audit/audit.go` -- SSE registration and central errors/actions.
- `cmd/mia/main.go` -- ensure tutor prompts, recover/start/stop manager, register summary handler and lifecycle owner.
- `api-doc/`, `docs/{api,database-layout,data-dir,jobs,tutoring-sessions}.md` -- concrete route/schema and persistence contracts.

## Tasks & Acceptance

**Execution:**
- [x] `migrations/000013_tutoring.up.sql`, `internal/tutoring/{types,store,service}.go` -- implement lifecycle,
  idempotency, authorization, immutable history, completion, review, and lifecycle callbacks.
- [x] `internal/material/retrieval.go`, `internal/tutoring/manager.go` -- implement normalized deterministic local search,
  bounded adjacent excerpts, per-request authorization, selected context, and used-material recording.
- [x] `internal/provider/openai/openai.go`, `internal/tutoring/manager.go` -- implement deadline-bounded streaming,
  persistence batching, queue dispatch, interruption, reconnection, startup recovery, and shutdown.
- [x] `internal/tutoring/summary.go` -- implement completely covered bounded summary generation and guarded correction-safe commit.
- [x] `internal/tutoring/handler.go`, shared registries, and `cmd/mia/main.go` -- register JSON:API/SSE operations and wiring.
- [x] `api-doc/*`, `docs/*`, tests -- document and verify the matrix, concurrency, budgets, scope, failures, and recovery.

**Acceptance Criteria:**
- Given one student on multiple devices, when concurrent valid starts occur, then exactly one active session exists and
  the other request receives the stable active-session conflict without creating duplicate data.
- Given intent-only tutoring, when the model requests material, then only authorized deterministic bounded excerpts reach
  it, rejected scopes return no content, and only delivered material is recorded used.
- Given a disconnect, provider partial failure, interruption, or restart, when the student reconnects or explicitly
  retries, then retained history is returned without duplicate generation and retry creates a linked response.
- Given an active versus completed session, when an assigned supervisor reads it, then active status exposes only start
  and activity instants while completed review exposes immutable chat and allows only attributed audited summary repair.

## Spec Change Log

## Design Notes

The manager owns synchronized response snapshots and bounded subscriber queues; SQLite remains durable truth. Material
owns all SQL and file access for retrieval, while tutoring supplies the student/course authorization context. Summary
work uses jobs-table leases; interactive response work never does.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all package and integration tests pass without live providers.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.
- `go test -race ./...` -- expected: manager, SSE, queue, recovery, and shutdown tests pass without races.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- expected: API contract passes.

## Suggested Review Order

**Session invariants and lifecycle**

- Start with transactional session, idempotency, queue, completion, and authorization behavior.
  [`service.go:43`](../../../../internal/tutoring/service.go#L43)

- Verify database-enforced active-session and response-slot invariants.
  [`000013_tutoring.up.sql:4`](../../../../migrations/000013_tutoring.up.sql#L4)

**Streaming and grounded context**

- Review durable generation ownership, persistence cadence, recovery, and shutdown.
  [`manager.go:164`](../../../../internal/tutoring/manager.go#L164)

- Check fixed token budgeting and newest-complete-turn context construction.
  [`manager.go:591`](../../../../internal/tutoring/manager.go#L591)

- Inspect provider event allowlisting, deadlines, tool rounds, and output limits.
  [`openai.go:94`](../../../../internal/provider/openai/openai.go#L94)

**Authorized material retrieval**

- Confirm bounded streaming search and deterministic ranking.
  [`retrieval.go:165`](../../../../internal/material/retrieval.go#L165)

- Confirm excerpt authorization, adjacency limits, and overlap deduplication.
  [`retrieval.go:233`](../../../../internal/material/retrieval.go#L233)

**HTTP and completed review**

- Review JSON:API and SSE route boundaries.
  [`handler.go:55`](../../../../internal/tutoring/handler.go#L55)

- Verify summary coverage and correction-safe guarded commit.
  [`summary.go:43`](../../../../internal/tutoring/summary.go#L43)

**Supporting contracts**

- Finish with concrete OpenAPI operations and stable errors.
  [`tutoring.yaml:1`](../../../../api-doc/paths/tutoring.yaml#L1)

- Review concurrency, authorization, recovery, and transport regression coverage.
  [`service_test.go:87`](../../../../internal/tutoring/service_test.go#L87)
