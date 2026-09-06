---
title: 'Text-to-speech'
type: 'feature'
created: '2026-09-06'
status: 'done'
baseline_commit: '89018104657f941757faf432c07c69d88ff5a62d'
review_loop_iteration: 0
followup_review_recommended: true
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
warnings: [oversized]
deferred: []
---

<!-- markdownlint-disable MD033 -->

<intent-contract>

## Intent

**Problem:** MIA stores each user's preferred ElevenLabs voice but cannot generate, retain, share, recover, or serve
speech for completed tutor responses.

**Approach:** Add the ElevenLabs provider boundary and `speech` vertical slice with a guarded SQLite cache, one shared
in-memory generation per cache identity, private MP3 publication/download, startup reconciliation, lifecycle deletion,
and concrete authenticated API operations.

## Boundaries & Constraints

**Always:** Enforce FR-72..73, NFR-1..3, NFR-13..18, and AD-3..7, AD-10, AD-11, and AD-14. Authorize each operation as
the owning student and accept only a completed tutor response. Require the requesting user's current stored voice ID,
bounded to 128 printable ASCII characters. Only an assigned supervisor sharing a course with a student-only account may
set, change, or clear that voice, and the student cannot self-edit it. Cache identity is response ID plus SHA-256
response-content digest plus voice; available cache is reused only before expiry. Concurrent requests return one row and
invoke ElevenLabs once. Read at most
25 MiB plus one byte, require an MP3 signature, publish mode 0600 by atomic rename before a state-guarded available commit,
and remove output after failed or stale commits. Use fixed 10-second response-header and two-minute total deadlines with
no automatic provider retry. Startup order is expired-row/file removal, stranded-generation failure without provider
work, required-file integrity validation, then orphan reconciliation. Access never changes expiry.

**Block If:** A required authorization, provider-data, retry, retention, or destructive-lifecycle behavior differs from
the binding contracts or cannot be implemented through existing table-owner APIs.

**Never:** Generate speech for queued, generating, interrupted, or failed responses; expose another student's response or
speech existence; retain provider payloads or partial output; trust provider media headers without signature validation;
route speech through `jobs`; retry provider calls automatically; discover provider voices; or let request cancellation
cancel generation shared by other callers.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Request | Owner, completed response, configured provider and voice | One generating resource and one provider call | Hidden response is not found; missing provider/voice is stable |
| Cache | Matching unexpired content digest and voice | POST/GET reuse row; GET serves MP3 when available | Expired/mismatched variants are unavailable and regenerable |
| Shared work | Concurrent identical POST requests | Both receive the same speech ID and generation | One provider failure marks the shared row failed |
| Recovery | Expired, stranded, missing, or orphaned file state at startup | Expired deleted, stranded failed, required missing file fatal, orphan removed | Never repeat an uncertain provider request |
| Retry | Explicit POST against failed variant | Same row changes to generating and runs once | No timer or background retry |

</intent-contract>

<!-- markdownlint-enable MD033 -->

## Code Map

- `migrations/000015_generated_speech.up.sql` -- own cache identity, state constraints, expiry, and response cascade.
- `internal/provider/elevenlabs/` -- allowlisted TTS request and bounded MP3 response with fixed deadlines and no retries.
- `internal/speech/` -- own scoped cache transitions, generation coordination, publication, startup recovery/integrity,
  binary serving metadata, lifecycle deletion, and tests.
- `internal/tutoring/service.go` -- export a narrow owner-scoped completed-response projection; no speech SQL reaches the
  tutoring tables directly.
- `internal/user/profile.go` -- export the user-owned stored TTS voice projection.
- `internal/filepublish/filepublish.go` -- reuse atomic private file publication and rollback.
- `internal/httpserver/{routes,errors}.go` -- reuse binary classification and add stable speech errors centrally.
- `cmd/mia/main.go` -- construct/reconcile/register speech after tutoring and register lifecycle scopes.
- `api-doc/`, `docs/{api,database-layout,data-dir,server-configuration}.md` -- publish route, state, audio, errors, and
  startup contracts.

## Tasks & Acceptance

**Execution:**

- [x] `migrations/000015_generated_speech.up.sql`, `internal/speech/{types,service,handler}.go` -- implement guarded cache,
  explicit retry, shared generation, startup order, downloads, and lifecycle cleanup.
- [x] `internal/provider/elevenlabs/{elevenlabs.go,elevenlabs_test.go}` -- implement/test fixed-deadline allowlisted MP3
  TTS.
- [x] `internal/{tutoring/service.go,user/profile.go,httpserver/errors.go}`, `cmd/mia/main.go` -- add narrow owner APIs,
  central errors, startup/wiring, and lifecycle registration.
- [x] `api-doc/*`, `docs/*`, `internal/speech/*_test.go` -- document and test scope, cache identity, concurrency, bounds,
  failure, retry, expiry, integrity, and transport behavior without live providers.

**Acceptance Criteria:**

- Given concurrent authorized requests for one completed response and unchanged voice/content, when speech is requested,
  then one cache row and one ElevenLabs request are shared and both callers observe the same speech ID.
- Given unavailable ElevenLabs, absent/invalid voice, ineligible response state, or wrong owner, when POST or GET is used,
  then stable sanitized errors are returned without provider work or existence disclosure.
- Given valid bounded MP3 output, when generation completes, then atomic publication precedes guarded availability and
  GET serves `audio/mpeg`; malformed, oversized, failed, or stale output leaves no published file and a failed/stale row.
- Given startup after expiry or interrupted generation, when reconciliation runs, then expired cache is removed,
  stranded work becomes failed without provider calls, missing unexpired available files fail startup, and orphans vanish.

## Spec Change Log

## Review Triage Log

### 2026-09-06 — Review pass

- intent_gap: 0
- bad_spec: 0
- patch: 5 (high 1, medium 4, low 0)
- defer: 0
- reject: 0
- addressed_findings:
  - `[high]` `[patch]` Retained generating IDs during concurrent orphan reconciliation so publication cannot create a
    missing-file available row.
  - `[medium]` `[patch]` Removed output after provider, publication, and shutdown failures.
  - `[medium]` `[patch]` Guarded deterministic file paths against malformed persisted speech identifiers.
  - `[medium]` `[patch]` Marked guarded commit failures terminal when the cache row still exists.
  - `[medium]` `[patch]` Added content-free audits for denied speech mutations.

## Design Notes

POST is an asynchronous command returning the current `generated-speech` resource (`202` while generating, `200` when
reusing an available or failed resource). GET returns the same JSON:API resource for generating/failed state and switches
to `audio/mpeg` for available state as required by the existing API narrative. The manager owns a process-lifetime context
so disconnecting the request that won generation does not cancel work shared with concurrent callers.

## Verification

**Commands:**

- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all tests pass without external providers.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.
- `go test -race ./...` -- expected: shared generation and reconciliation tests pass without races.
- `npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml` -- expected: API contract passes.

## Auto Run Result

Status: done

Implemented the ElevenLabs adapter, generated-speech migration and vertical slice, shared asynchronous generation,
explicit failed-state retry, bounded atomic MP3 publication, retention expiry, startup recovery and integrity checks,
private state/audio routes, stable errors, content-free failure/denial audits, and lifecycle cleanup. Added narrow tutoring
and user owner APIs and split tutor startup recovery so global startup ordering remains compliant.

Tests cover provider request allowlisting, cancellation, malformed and oversized output, unavailable degradation,
authorization hiding, concurrent cache sharing, explicit retry, expiry without access extension, startup reconciliation,
lifecycle cleanup, and JSON:API/audio route behavior. Go tests, vet, golangci-lint, race tests, OpenAPI lint, and Markdown
lint passed. Five review patches were applied (high 1, medium 4, low 0; score 13), so a follow-up review is recommended.

Residual risk: the provider model's own text limits remain external service behavior; a provider rejection is retained as
a sanitized failed generation and requires an explicit authorized retry, as required.
