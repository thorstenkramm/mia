---
title: 'Profiles, avatars, and mobile verification'
type: 'feature'
created: '2026-08-31'
status: 'done'
baseline_revision: '78348822debcff46a35bc72a93f9a339255ba844'
review_loop_iteration: 1
followup_review_recommended: false
context:
  - '{project-root}/AGENTS.md'
  - '{project-root}/_bmad-output/specs/spec-mia/SPEC.md'
  - '{project-root}/_bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md'
  - '{project-root}/_bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md'
warnings: [oversized]
deferred: []
---

<intent-contract>

## Intent

**Problem:** Staff cannot read or edit their non-security profile, manage a verified mobile, or upload a safe avatar.
The course slice also needs one reusable image pipeline before it exposes course logos.

**Approach:** Extend the `user` vertical slice with current-user profile, mobile-verification, and avatar routes. Add
one kernel file-publication helper and one bounded JPEG/PNG normalizer shared by avatars now and course logos later.

## Boundaries & Constraints

**Always:** Enforce FR-33, FR-35, FR-36, NFR-1, NFR-3, NFR-13, NFR-14, AD-3, AD-4, AD-10, AD-11, and AD-12.
Only accounts with a staff role may self-edit. Writable profile fields are name, nickname, preferred language, country,
time zone, and TTS voice; optional text uses null for absence. Mobile codes are six digits, single-use, expire after 30
minutes, invalidate after five failures, and use the shared account/destination SMS send limits. Mobile change or removal
deletes pending SMS MFA enrollment/replacement state without changing an active SMS factor destination. Store and serve
only a metadata-free, non-animated PNG of at most 512 pixels per dimension from a signature-validated JPEG/PNG source of
at most 10 MiB, 40 megapixels, and 10,000 pixels per dimension. Audit profile/mobile/avatar mutations without values.

**Block If:** Implementation requires a profile field, provider outcome, authorization boundary, or image lifecycle not
defined by FR-33..36 and the current human-readable contracts.

**Never:** Make username, staff email, roles, password/security state, or ban state profile-writable; allow student-only
self-editing; return or log mobile codes or numbers; move an active SMS factor destination; retain or serve image source
bytes; expose filesystem paths; add course or supervisor-student authorization before their owning stories.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
| --- | --- | --- | --- |
| Staff profile | Authenticated staff GET/PATCH | Return profile; atomically validate, update, and audit allowed fields | Reject protected, unknown, malformed, or student-self fields |
| Mobile challenge | Staff and valid new E.164 destination | Replace pending challenge, reserve durable quota, send one hidden code | Stable unavailable on no SMS/provider failure; current mobile unchanged |
| Verify/resend/remove | Current challenge or verified mobile | Verify once, resend same code, or remove without SMS; clear pending SMS MFA | Wrong/expired code preserves current mobile; fifth failure invalidates |
| Avatar upload | Bounded JPEG/PNG including oriented JPEG | Store only fitted metadata-free PNG by atomic replacement | Reject wrong signature, animation, malformed, or oversized dimensions/bytes |
| Avatar read/delete | Authorized current user | Serve PNG with safe private headers/ETag, or remove it idempotently | Missing image returns the ordinary not-found response |

</intent-contract>

## Code Map

- `internal/user/user.go` -- owns user/profile records and the MFA-profile read; extend with staff-scoped profile and
  mobile mutations while deleting auth-owned pending SMS enrollments only through an exported auth API.
- `internal/user/handler.go` -- add current-user JSON:API profile/mobile routes and avatar upload/download/delete handlers.
- `internal/auth/auth.go` -- expose a narrow transaction-aware operation for invalidating pending SMS enrollments; active
  `mfa_factors.sms_destination` remains immutable.
- `internal/provider/sms/sms.go` -- reuse the durable account/destination gate and sender boundary; wire configured
  ClickSend delivery rather than leaving mobile verification permanently unavailable.
- `internal/imagefile/` -- new shared bounded decode/orientation/resize/PNG normalization used by avatars and future logos.
- `internal/filepublish/` -- first file-writing slice's shared mode-0600 temporary-write and atomic-replacement helper.
- `migrations/000008_profiles.up.sql` -- add nullable profile columns, update attribution, and mobile challenge storage.
- `internal/httpserver/httpserver.go` -- add authenticated PATCH/PUT registration and preserve mandatory auth reload.
- `internal/httpserver/errors.go`, `internal/audit/audit.go` -- register user profile/mobile/image errors and content-free
  mutation actions in the central registries.
- `cmd/mia/main.go` -- wire user routes with data directory and configured SMS adapter.
- `docs/api.md`, `docs/database-layout.md`, `docs/data-dir.md` -- keep concrete route, schema, and file-publication
  contracts aligned if implementation details differ from their existing draft.

## Tasks & Acceptance

**Execution:**
- `migrations/000008_profiles.up.sql`, `internal/user/*.go` -- implement staff profile persistence and routes, mobile
  challenge lifecycle, avatar authorization, and same-transaction audit behavior.
- `internal/imagefile/*`, `internal/filepublish/*` -- implement and test bounded signature decoding, EXIF orientation,
  animation rejection, aspect-preserving fit, metadata-free PNG output, permissions, and atomic replacement.
- `internal/provider/sms/*`, `internal/auth/auth.go` -- provide configured ClickSend sends, shared durable reservation, and
  an ownership-preserving pending-SMS-enrollment invalidation API.
- `internal/httpserver/{httpserver,errors}.go`, `internal/audit/audit.go`, `cmd/mia/main.go` -- register methods, central
  errors/actions, dependencies, and routes without bypassing the authenticated registration path.
- `internal/user/*_test.go`, `internal/imagefile/*_test.go`, `internal/provider/sms/*_test.go` -- exercise the matrix,
  protected-field rejection, staff/student authorization, code secrecy and limits, image bombs/signatures/orientation,
  metadata stripping, safe download headers, and failed-publication cleanup without external providers.

**Acceptance Criteria:**
- Given a staff account, when it PATCHes only documented profile fields, then normalized values are persisted and audited;
  given a student-only account or protected field, the same self-edit operation is rejected without mutation.
- Given configured SMS and a staff user, when a mobile challenge is successfully verified, then the new E.164 number
  becomes verified exactly once and pending SMS enrollment/replacement state is deleted while the active destination stays.
- Given a valid JPEG or PNG under every bound, when staff uploads an avatar, then later download returns only a fitted,
  metadata-free PNG with safe private headers; malformed, animated, wrong-signature, and oversized inputs are rejected.

## Spec Change Log

## Review Triage Log

## Design Notes

Student profile administration remains in story 8 because shared-course authorization and memberships do not yet exist.
Course-logo HTTP operations remain in story 7 because the course package owns course visibility and mutation scope; this
story delivers the complete reusable normalization/publication pipeline that those routes must call.

## Verification

**Commands:**
- `gofmt -w <changed Go files>` -- expected: all changed Go files are formatted.
- `go test ./...` -- expected: all unit and integration tests pass without live provider calls.
- `go vet ./...` -- expected: no findings.
- `golangci-lint run ./...` -- expected: no findings.

## Auto Run Result

Status: done

Implemented staff current-user profile editing, SMS-confirmed mobile changes, normalized avatar publication, the
shared image and file-publication kernels, and configured ClickSend delivery. Added integration and boundary tests for
authorization, code secrecy and lifecycle, image validation and normalization, safe avatar serving, and publication
rollback.

Verification passed: `go test ./...`, `go vet ./...`, `golangci-lint run ./...`, and `go test -race ./...`.
