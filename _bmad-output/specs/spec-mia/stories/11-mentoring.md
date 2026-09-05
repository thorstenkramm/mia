---
title: 'Mentoring'
type: 'feature'
created: '2026-09-05'
status: 'done'
baseline_commit: '2000c326d1cdefab369803ce2b583d414e9ce3c6'
review_loop_iteration: 2
followup_review_recommended: false
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
deferred: []
---

<!-- markdownlint-disable MD033 -->
<intent-contract>

## Intent

**Problem:** MIA has no mentor assignment or mentoring-work persistence, so last-resource escalation, supervisor triage,
external scheduling, and transactional mentor-removal recovery are unavailable.

**Approach:** Add the `mentoring` vertical slice, its assignment and session tables, scoped JSON:API operations,
content-free audits, lifecycle callbacks, and the user-owned request-permission field required by FR-66.

## Boundaries & Constraints

**Always:** Enforce FR-65..71, NFR-1..3 and NFR-13, plus AD-3, AD-4, AD-7, AD-12, and AD-14. New requests require an
active joined course, `mentoring_requests_allowed`, and a current student mentor assignment, but are always unassigned.
Only an assigned supervisor manages course/student mentor assignments and triages or directly reassigns open work. A
mentor must have the permanent mentor role, then course and student assignment, before selection. Only the current mentor
responds or initially schedules; the owning student or current mentor directly reschedules a future schedule. Apply the
documented cancellation and completion actor/time rules. Preserve prior response/authorship on direct reassignment and
mentor removal; removal atomically clears the current mentor, proposed/scheduled times, and meeting details on open work.
Mentors receive the topic and mentoring record only, never uploads, briefs, or tutoring chat. All instants use shared UTC
parsing/formatting. Bound and normalize topic, response, and meeting instructions; accept HTTPS meeting URL metadata only
and never fetch it.

**Block If:** A required authorization, lifecycle, closure, or data-retention rule is not fixed by FR-65..71 and the
current product/database/API contracts.

**Never:** Treat model output as authorization or persist a claim that tutoring approaches were exhausted; add live
mentor chat, notifications, attendance/no-show state, mentor self-removal, acceptance workflows, external URL fetching,
or access to private uploads/chat history. Course deactivation must not block existing work.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Assignment | Assigned supervisor and eligible mentor/student | Immediate idempotent course and student assignments | Hidden missing/wrong-scope targets fail without partial writes |
| Request | Owning student, active course, permission, assigned mentor | Unassigned request with optional proposed UTC instant | Missing gate rejects without exposing assignment details |
| Triage/response | Supervisor selects assigned mentor; mentor responds/schedules | Topic-only work proceeds and schedule/details become visible | Unassigned/wrong mentor or closed work rejects |
| Closure/schedule | Authorized actor and eligible current state/time | Cancel, reschedule, or complete with actor/time audit | Past reschedule, late cancellation, or early completion rejects |
| Removal | Supervisor removes student/course mentor assignment | Open work returns to triage in the same transaction | Closed history remains; no response/topic loss |

</intent-contract>
<!-- markdownlint-enable MD033 -->

## Code Map

- `migrations/000014_mentoring.up.sql` -- add the user permission, course/student mentor assignments, mentoring session
  state constraints, indexes, and actor foreign-key behavior.
- `internal/mentoring/` -- own assignment/session SQL, authorization-scoped operations, validation, resources, lifecycle
  callbacks, and tests.
- `internal/course/course.go` -- export narrow transaction-aware course assignment/membership checks; retain ownership of
  course and membership tables.
- `internal/user/user.go` -- own transaction-aware request-permission reads/writes and mentor-role eligibility.
- `internal/course/handler.go` -- expose the supervisor-managed student request-permission through the existing student
  administration route family without broadening writable profile scope.
- `internal/httpserver/errors.go`, `internal/audit/audit.go` -- register stable mentoring errors and content-free assignment,
  triage, response, schedule, cancellation, completion, removal, and denied-mutation actions.
- `cmd/mia/main.go` -- construct/register mentoring routes and all three lifecycle scopes after dependencies exist.
- `api-doc/{openapi.yaml,paths/mentoring.yaml,schemas/{requests,resources}/...}` -- define concrete assignment, session,
  PATCH, pagination, security, bounds, and stable-error transport contracts.
- `docs/{api,database-layout}.md` -- replace planned/open mentoring transport language with the implemented contract.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000014_mentoring.up.sql`, `internal/mentoring/{types,service}.go` -- implement assignment, request,
  triage, response/scheduling, cancellation/completion, removal, and lifecycle invariants transactionally.
- [x] `internal/course/course.go`, `internal/user/user.go`, `internal/course/handler.go` -- expose owner APIs and the
  supervisor-controlled mentoring permission needed for request admission.
- [x] `internal/mentoring/handler.go`, shared registries, and `cmd/mia/main.go` -- register authenticated JSON:API routes,
  central errors/audits, lifecycle owners, and wiring.
- [x] `api-doc/*`, `docs/*`, `internal/mentoring/*_test.go`, route tests -- document and verify all matrix states,
  authorization scopes, input/time bounds, existence hiding, and atomic removal behavior.

**Acceptance Criteria:**

- Given no student mentor assignment or disabled request permission, when a student attempts a new request, then no row
  is created while existing rows remain visible and cancellable.
- Given a valid unassigned request, when an assigned supervisor selects one of that student's assigned mentors, then only
  that mentor may respond/schedule and receives no tutoring or upload content.
- Given scheduled work, when authorized actors reschedule, cancel, or complete it, then state, actor, and UTC instant rules
  are enforced and content-free audits commit with each mutation.
- Given an open record assigned to a removed mentor, when either student or course assignment is removed, then assignment
  deletion and triage commit atomically while topic and immutable prior response/authorship survive.
- Given direct supervisor reassignment to another assigned mentor, when it commits, then schedule, meeting details,
  response, and response authorship remain unchanged.

## Spec Change Log

## Review Triage Log

### 2026-09-05 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 3 (high 2, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[high]` `[patch]` Added assignment-scoped minimal student identity and avatar routes with immediate revocation.
  - `[high]` `[patch]` Combined mentoring-session read authorization and loading in one scoped SQL statement.
  - `[medium]` `[patch]` Classified nullable and wrongly typed PATCH action attributes as `422 mentoring_invalid`.

### 2026-09-05 — Follow-up review pass

- intent_gap: 0
- bad_spec: 0
- patch: 3 (high 1, medium 1, low 1)
- defer: 0
- reject: 0
- addressed_findings:
  - `[high]` `[patch]` Required a current student-mentor assignment for mentor access to open and closed records.
  - `[medium]` `[patch]` Split assignment request schemas so each operation fixes its exact JSON:API resource type.
  - `[low]` `[patch]` Consolidated mentoring collection authentication, pagination, translation, and encoding plumbing.

## Design Notes

One `mentoring_sessions` row is the durable request, response, appointment, and closure. PATCH uses presence-aware fields:
supervisors change the current mentor relationship; the current mentor may add response and schedule/details; student or
mentor may reschedule; closure uses `closure_reason`. Actor permissions are derived from current database state for every
request. Mentor-removal triage is a dedicated service operation, never modeled as direct reassignment.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all tests pass without external providers.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- expected: API contract passes.

## Auto Run Result

Status: done

Implemented course and student mentor assignment, the student request gate, unassigned request creation, supervisor
triage and reassignment, mentor response and scheduling, participant rescheduling and cancellation, mentor completion,
and lifecycle cleanup. Mentor removal atomically returns open work to triage while preserving prior response and
authorship, and tutoring context now exposes only the current mentoring-availability gate. Explicitly assigned mentors
can retrieve only the student's username, name, nickname, and avatar; assignment removal revokes the projection.

Mutation, denial, triage, schedule, reschedule, closure, assignment, and permission changes produce content-free audit
events. Reschedule audits retain the required previous and new UTC instants. The authenticated JSON:API routes, stable
errors, OpenAPI schemas, persistence documentation, and authorization and workflow tests are complete.

Verification passed: `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...`, OpenAPI lint,
and Markdown lint.
