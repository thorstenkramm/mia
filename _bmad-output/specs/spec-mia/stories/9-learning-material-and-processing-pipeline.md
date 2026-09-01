---
title: 'Learning material and processing pipeline'
type: 'feature'
created: '2026-09-01'
status: 'done'
baseline_revision: '39c73c2b12d2aa6cb14f14979e1f8ccc78a3acb1'
review_loop_iteration: 0
followup_review_recommended: true
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** MIA cannot store, process, approve, or safely expose learning material, and has no durable worker or job
oversight. Consequently course activation remains permanently blocked and CAP-9/CAP-16 are unavailable.

**Approach:** Add the material vertical slice, jobs kernel, Mistral OCR and OpenAI summary adapters, bounded local
extractors, deterministic private file publication, lifecycle integration, and authenticated material/job APIs.

## Boundaries & Constraints

**Always:** Enforce FR-45..53, FR-83..84, NFR-1..3, NFR-13..15, AD-3..7, AD-10..12, and AD-14. Scope every read and
mutation by course assignment, membership, ownership, state, and action. Freeze the complete file set atomically at
finalization; one failed extraction makes the whole material failed. Use one leased worker, count interrupted attempts,
retry transient failures at most three attempts, and guard every asynchronous commit by lease token and target state.
Publish source and normalized content outside the static root through the shared atomic publisher. Validate strict
content JSONL and brief schemas and all documented limits. Link-only material is course-wide metadata and is never
fetched. Brief correction on approved material atomically revokes approval; generated work never overwrites a human
brief. Wire qualifying material readiness into course activation.

**Block If:** A missing authorization rule, provider classification, lifecycle transition, or file-integrity behavior is
required but not fixed by the PRD, addendum, architecture spine, or provider contract.

**Never:** Add generic job retry, expose diagnostics to supervisors, let administrator role imply course scope, accept
extension-only validation, send DOCX/text/Markdown to Mistral, retain raw provider payloads, approve partial processing,
serve internal paths, fetch website/YouTube links, or regenerate a supervisor-edited ready brief.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Upload | Authorized draft and bounded supported file | Validated source and row commit together | Invalid, mixed-format, oversized, or unsafe source is rejected and unpublished |
| Finalize | Frozen valid file set or valid link metadata | Extraction jobs or direct link readiness commit atomically | Empty private source, incomplete link brief, or invalid state changes nothing |
| Worker | Due job with current lease | Content or brief commits once and usage accumulates | Transient errors back off; permanent/exhausted failure faults the whole material |
| Brief approval | Assigned supervisor and ready course-wide material | Correction is validated; approval is explicit | Changed approved brief revokes approval; private approval is rejected |
| Download | In-scope actor and visible source/content | Safe bounded stream with nosniff/download headers | Unknown and out-of-scope are indistinguishable |
| Oversight | Administrator or assigned supervisor | Admin sees diagnostics; supervisor sees resource-safe state | Unsupported filters or out-of-scope subjects fail safely |

</intent-contract>

## Code Map

- `migrations/000012_materials_jobs.up.sql` -- material, source-file, and jobs ownership with state constraints,
  subject columns, lease fields, usage counters, and uniqueness guards.
- `internal/jobs/` -- queue owner: enqueue, atomic claim, renewal, guarded completion/failure, startup recovery,
  shutdown, lifecycle deletion, and administrator/safe-subject reads.
- `internal/material/` -- material owner: scoped CRUD, upload validation, finalization, brief schema, approval,
  extraction handlers, file paths, downloads, lifecycle callbacks, and HTTP resources.
- `internal/provider/mistral/` -- fixed-deadline allowlisted OCR adapter with retry classification.
- `internal/provider/openai/` -- fixed-deadline structured-summary adapter with allowlisted response/usage parsing.
- `internal/filepublish/filepublish.go` -- reuse reversible same-directory publication for source/content commits.
- `internal/course/course.go` -- reuse transaction-aware `MaterialReadiness`; no material SQL in course.
- `internal/lifecycle/lifecycle.go` -- register material and jobs course/student/account deletion through wiring.
- `internal/httpserver/errors.go`, `internal/audit/audit.go` -- extend central error and content-free action registries.
- `cmd/mia/main.go` -- construct adapters/worker, register handlers/deleters, wire readiness, and coordinate shutdown.
- `docs/api.md`, `docs/database-layout.md`, `docs/data-dir.md`, `docs/jobs.md` -- align implemented contracts.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000012_materials_jobs.up.sql`, `internal/jobs/*` -- implement durable leased execution and oversight.
- [x] `internal/provider/{mistral,openai}/*` -- implement bounded provider contracts using local HTTP fakes in tests.
- [x] `internal/material/*` -- implement material state, validation, extraction, strict briefs, authorization, and routes.
- [x] `internal/httpserver/errors.go`, `internal/audit/audit.go`, `cmd/mia/main.go` -- register and wire the complete slice.
- [x] `docs/*`, `mia.example.toml` -- document concrete API, storage, provider, and upload behavior.
- [x] `internal/{jobs,material,provider}/*_test.go` -- cover the matrix, retries, leases, guarded commits, and scope.

**Acceptance Criteria:**

- Given an authorized uploader and valid supported sources, when material is finalized and jobs complete, then every file
  has strict content JSONL, one grounded valid brief exists, and the material becomes ready only as one complete unit.
- Given a transient provider failure, when the worker handles the job, then attempts are bounded to three with fixed
  delays and only the current lease can publish durable output; permanent failures do not retry.
- Given approved course-wide material, when a supervisor submits changed valid brief content, then approval and its
  attribution are atomically cleared until an explicit re-approval.
- Given an out-of-scope actor, when any source, content, private brief, or safe processing route is requested, then the
  response is indistinguishable from an unknown resource.
- Given at least one approved ready file-backed course-wide material, when course activation checks readiness, then the
  material owner reports true; link-only or unapproved material never qualifies.

## Spec Change Log

## Review Triage Log

### 2026-09-01 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 2: (high 1, medium 1, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[high]` `[patch]` Startup recovery considered only already-expired leases, so an immediate restart could strand a
    running job until no recovery path remained. Recovery now reclaims every running job under the exclusive process
    lock and has a regression test for an unexpired lease.
  - `[medium]` `[patch]` The oversight matrix lacked a direct test proving administrator diagnostics stay separate from
    safe subject-scoped state. Added coverage for administrator authorization, diagnostic visibility, and redaction.

## Design Notes

Job handlers are registered by type in command wiring. They return classified errors and commit through jobs-owned lease
guards while material owns subject transitions. Source paths derive only from validated prefixed UUIDs. Provider response
objects are decoded into small allowlisted transport types and discarded after normalized output is validated.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all tests pass without live providers.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.
- `go test -race ./...` -- expected: worker lease/shutdown and publication synchronization tests pass.

## Auto Run Result

### Summary

Implemented the complete learning-material vertical slice and its durable background-job kernel. The change adds
bounded source validation and private publication, local extraction and OCR, structured grounded briefs, approval and
visibility rules, lifecycle cleanup, startup reconciliation, course-readiness integration, provider adapters, safe
downloads, and administrator or subject-scoped job oversight.

### Files changed

- `migrations/000012_materials_jobs.up.sql` -- adds material, source-file, and durable-job storage.
- `internal/jobs/` -- owns leased execution, startup recovery, retries, guarded commits, and oversight APIs.
- `internal/material/` -- owns material authorization, processing, files, briefs, approval, reconciliation, and routes.
- `internal/provider/mistral/`, `internal/provider/openai/` -- add bounded allowlisted provider adapters and fake tests.
- `internal/lifecycle/lifecycle.go`, `internal/course/course.go` -- add transactional deletion and post-commit cleanup
  integration plus material readiness.
- `internal/audit/audit.go`, `internal/httpserver/errors.go` -- register content-free audit actions and API errors.
- `cmd/mia/main.go` -- wires provider clients, material services, worker lifecycle, routes, and readiness.
- `internal/sqlite/sqlite_test.go`, `go.mod`, `go.sum` -- account for the migration and extraction/tokenizer dependencies.
- `docs/api.md`, `docs/database-layout.md`, `docs/jobs.md` -- document implemented API, persistence, and worker behavior.

### Review findings

- Patches applied: 2 (high 1, medium 1, low 0).
- Items deferred: 0.
- Items rejected: 0.
- Follow-up review recommendation: `true`; score is 3 from one medium finding, with one high finding independently
  requiring the recommendation.

### Verification performed

- `gofmt -w <changed Go files>` -- passed.
- `go test ./...` -- passed.
- `go vet ./...` -- passed.
- `golangci-lint run ./...` -- passed with 0 issues.
- `go test -race ./...` -- passed.
- Matrix audit -- upload/finalization, worker retry and lease guards, correction/approval, scoped download, and separated
  oversight behavior are covered by tests that ran in the commands above.

### Residual risks

Provider behavior is verified through bounded local HTTP fakes rather than live paid services, as required. Operational
compatibility still depends on the configured providers continuing to honor their documented request and response
contracts.
