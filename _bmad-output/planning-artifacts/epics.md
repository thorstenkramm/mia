---
stepsCompleted:
  - step-01-validate-prerequisites
  - step-02-design-epics
  - step-03-create-stories
  - step-04-final-validation
inputDocuments:
  - _bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/prd.md
  - _bmad-output/planning-artifacts/prds/prd-mia-2026-08-29/addendum.md
  - _bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md
  - docs/architecture.md
  - docs/product-requirements.md
  - api-doc/openapi.yaml
  - frontend-ux/DESIGN.md
  - frontend-ux/EXPERIENCE.md
  - frontend-ux/FRONTEND-PRD-FOUNDATION.md
  - frontend-ux/BACKEND-CONTRACT-GAPS.md
  - frontend-ux/FRONTEND-STATE-MACHINES.md
  - frontend-ux/REQUIREMENT-FLOW-MATRIX.md
  - frontend-ux/SURFACE-STATE-MATRIX.md
  - frontend-ux/ACCESSIBILITY-ACCEPTANCE.md
  - frontend-ux/SECURITY-PRIVACY-ACCEPTANCE.md
  - frontend-ux/ROUTE-INVENTORY.md
  - frontend-ux/validation-report.md
---

# MIA - Epic Breakdown

## Overview

This artifact records the completed Epic 1 baseline and the approved Epic 2 requirements, delivery order, stories, and
acceptance criteria for reliable browser-ready backend workflows.

## Input Sources

Backend paths are relative to the MIA repository root. `[PRD]` is the backend PRD and addendum. `[ARCH]` is the
architecture spine and `docs/architecture.md`. `[PRODUCT]` is `docs/product-requirements.md`, and `[API]` is
`api-doc/openapi.yaml`.

`frontend-ux/` denotes the finalized UX package at
`../mia-frontend/_bmad-output/planning-artifacts/ux-designs/ux-mia-frontend-2026-09-12/`. `[UX]` covers that complete
package, while `[BCG]` refers specifically to `BACKEND-CONTRACT-GAPS.md`.

## Requirements Inventory

### Functional Requirements

- FR-1: Usernames contain 3-32 allowed ASCII characters, preserve display case, and compare uniquely by ASCII
  lowercase. [PRD 6.1]
- FR-2: Emails use the bounded practical ASCII grammar, are trimmed, and compare uniquely by ASCII lowercase when
  present. [PRD 6.1]
- FR-3: Every account has credentials, language, country, and time zone; staff also have verified email. [PRD 6.1]
- FR-4: Optional profile fields use null only and enforce the confirmed code-point, byte, year, and E.164 bounds.
  [PRD 6.1]
- FR-5: Descriptive text is valid normalized UTF-8 with forbidden controls rejected; passwords and chats are unchanged.
  [PRD 6.1]
- FR-6: MIA has no public signup; staff use invitations and assigned supervisors provision students. [PRD 6.2]
- FR-7: The first administrator is created only by the one-time interactive offline bootstrap command. [PRD 6.2]
- FR-8: Invitations are single-use, scopeless new-account registrations whose invitees choose credentials and profile.
  [PRD 6.2]
- FR-9: Invitations do not expire; definite SMTP failure makes them faulty, while timeout leaves them pending and usable.
  [PRD 6.2]
- FR-10: Invitation deletion revokes pending, deletes faulty, rejects terminal states, and obeys management scope;
  resend rotates the token. [PRD 6.2]
- FR-11: Public invitation preview reveals only MIA and role; all invalid or consumed token states are indistinguishable.
  [PRD 6.2]
- FR-12: Invitation acceptance atomically creates the account and exact invited role set without consuming on email
  conflict. [PRD 6.2]
- FR-13: Authorized direct grants target registered verified staff by user ID, are immediate and idempotent, and apply
  coupled supervisor-to-student role semantics. [PRD 6.2]
- FR-14: Invitation and reset tokens are canonical lowercase UUID v4 values stored only by SHA-256 digest and carried in
  URL fragments. [PRD 6.2]
- FR-15: An assigned supervisor provisions a student into an active course with a temporary password that must be
  replaced at first login. [PRD 6.3]
- FR-16: Existing students join active courses by complete username only, with no search, disclosure, role grant, or
  acceptance workflow. [PRD 6.3]
- FR-17: A removed student may rejoin an active course as a new membership, without restoration of deleted course data.
  [PRD 6.3]
- FR-18: Passwords enforce the fixed 12-128 code-point and 512-byte policy and local common-password rejection without
  trimming or mutation. [PRD 6.4]
- FR-19: A shared-course supervisor can set a student-only temporary password, invalidate every cookie, and require
  replacement without recording the value. [PRD 6.4]
- FR-20: Staff recovery by username is existence-hiding; reset links are single-use for 30 minutes, and success
  invalidates sibling challenges but not existing MVP cookies. [PRD 6.4]
- FR-21: Definite recovery SMTP failure invalidates the challenge, timeout leaves it usable without retry, and security
  email is sanitized English plain text. [PRD 6.4]
- FR-22: Stateless browser sessions expire after 30 idle minutes and 12 absolute hours; qualifying authenticated actions
  reset idle only. [PRD 6.5]
- FR-23: Every request reloads account and authorization state; deletion, bans, and security-generation mismatch take
  effect on the next request. [PRD 6.5]
- FR-24: Login uses password, then restricted MFA, then restricted password change, with fixed expiry, permitted actions,
  and cookie rotation. [PRD 6.5]
- FR-25: Logout clears only the current browser cookie, and concurrent browser sessions remain allowed. [PRD 6.5]
- FR-26: MFA is optional for every account and permits at most one active TOTP or SMS factor. [PRD 6.6]
- FR-27: TOTP and SMS factors enforce the fixed algorithms, code formats, expiry, verification, provider, and immutable
  destination rules. [PRD 6.6]
- FR-28: Enrollment and login challenges expire after 30 minutes and five failures; SMS resend reuses the code and fixed
  expiry and limits. [PRD 6.6]
- FR-29: MFA activation issues ten one-time recovery codes shown once and stored non-reversibly; replacement invalidates
  old codes. [PRD 6.6]
- FR-30: MFA disable or replacement consumes one five-minute proof atomically; replacement activates only after the new
  factor verifies. [PRD 6.6]
- FR-31: Password, MFA, ban-state, or account-state changes invalidate outstanding MFA challenges and proofs. [PRD 6.6]
- FR-32: Authorized student or staff lost-factor reset removes MFA and codes and applies the exact cookie and password
  gates; sole-admin recovery is offline only. [PRD 6.6]
- FR-33: Staff may edit only the confirmed self-profile fields and remove mobile; immutable and security fields remain
  unwritable. [PRD 6.7]
- FR-34: Students cannot edit profiles; a shared-course supervisor may edit a student-only non-security profile with
  global effect and defined mobile invalidation. [PRD 6.7]
- FR-35: Staff mobile changes require bounded SMS verification, preserve active-factor destination, and are unavailable
  without SMS. [PRD 6.7]
- FR-36: Avatar and logo uploads enforce signature, size, dimension, decode, normalization, storage, and inherited
  authorization rules. [PRD 6.7]
- FR-37: Courses are inactive or active, have globally normalized unique names, and begin inactive and student-hidden.
  [PRD 6.8]
- FR-38: Administrators create courses with supervisors atomically; every course retains at least one supervisor.
  [PRD 6.8]
- FR-39: Activation requires goals, tutor instructions, language, a supervisor, and approved ready file-backed material;
  only active courses accept memberships and sessions. [PRD 6.8]
- FR-40: Loss of the last qualifying material blocks new sessions without deactivating the course and does not stop
  existing sessions. [PRD 6.8]
- FR-41: Assigned supervisors may deactivate; deactivation blocks new sessions and memberships, and reactivation
  rechecks all prerequisites. [PRD 6.8]
- FR-42: An assigned supervisor may remove a student only without an active course session, deleting all course-scoped
  student data but retaining account and other-course data. [PRD 6.8]
- FR-43: Only an administrator may delete an inactive course with no active sessions, removing course-scoped data only.
  [PRD 6.8]
- FR-44: Supervisors may join and test their own active courses as students; no preview or inactive-course session exists.
  [PRD 6.8]
- FR-45: Material has immutable scope and format, normalized unique course-local names, strict accepted formats, and
  hardened format-specific validation. [PRD 6.9]
- FR-46: Course-wide material requires ready non-empty briefs for revocable approval; meaningful approved brief edits
  atomically revoke approval. [PRD 6.9]
- FR-47: Student-private material is unapproved, permanently private, and visible only to owner, tutor, and assigned
  supervisors. [PRD 6.9]
- FR-48: Finalization freezes files, creates bounded extraction work, makes any file failure visible, and permits draft
  repair only after terminal failure. [PRD 6.9]
- FR-49: Every material has a grounded structured brief within all specified bounds; supervisor edits are protected from
  regeneration. [PRD 6.9]
- FR-50: Upload, page, file, image, and DOCX expansion settings remain within fixed hard caps, with no unbounded path.
  [PRD 6.9]
- FR-51: Website and YouTube material stores validated HTTPS metadata only, never fetches it, and cannot satisfy
  source-backed readiness. [PRD 6.9]
- FR-52: A student may delete unused private material, removing retrievable source and brief while preserving completed
  history and content-free audit. [PRD 6.9]
- FR-53: Every material download is authorized, uses safe response headers, and serves files outside the public root.
  [PRD 6.9]
- FR-54: Session start validates joined active course and optional material eligibility and enforces one active session
  per student globally. [PRD 6.10]
- FR-55: Tutoring sessions do not idle-expire, resume across devices, and can be finished only by their owner while no
  work is active or queued. [PRD 6.10]
- FR-56: Session and message UUID v4 request IDs provide strict payload-bound idempotency for the owning data lifetime.
  [PRD 6.10]
- FR-57: Tutor context uses the fixed ordered instruction, course, student, selected-material, and prior-summary inputs
  with bounded complete-content inclusion. [PRD 6.10]
- FR-58: Every tutor request uses fixed 32,000-input and 2,048-output token budgets with deterministic context priority.
  [PRD 6.10]
- FR-59: Tutor retrieval is locally searched, bounded, deterministic, independently authorized, and records use only
  when content reaches the model. [PRD 6.10]
- FR-60: Messages are bounded; a session permits one generating response and one queued message, streams output, and
  persists it at bounded intervals. [PRD 6.10]
- FR-61: Disconnect does not cancel work; reconnect resumes it, explicit stop preserves received text, and provider
  failure remains visible without automatic retry. [PRD 6.10]
- FR-62: Explicit retry creates linked history only for failed or interrupted work in an active idle session; completed
  sessions reject new work. [PRD 6.10]
- FR-63: Completion atomically queues summary work; authorized students and supervisors receive state-appropriate access,
  correction, attribution, and regeneration. [PRD 6.10]
- FR-64: Startup resumes queued tutor work but fails uncertain generation; shutdown preserves queued work and boundedly
  cancels running work. [PRD 6.10]
- FR-65: The tutor may suggest mentoring only after trying the required educational approaches and authorized material.
  [PRD 6.11]
- FR-66: New mentoring requires an assigned mentor, active course, and independent student permission; existing work
  remains available when creation is disabled. [PRD 6.11]
- FR-67: Supervisors make immediate course and student mentor assignments; mentors cannot reject them and receive only
  authorized mentoring data. [PRD 6.11]
- FR-68: Requests begin unassigned; supervisor assignment precedes mentor response, scheduling, and external meeting
  metadata. [PRD 6.11]
- FR-69: Cancellation, rescheduling, and completion obey actor and lifecycle rules; only the assigned mentor completes
  after the scheduled instant. [PRD 6.11]
- FR-70: Mentor removal atomically returns work to triage and clears future details, while direct reassignment preserves
  schedule, details, response, and authorship. [PRD 6.11]
- FR-71: Course deactivation blocks new mentoring requests but not authorized handling of existing work. [PRD 6.11]
- FR-72: On-demand MP3 speech is limited to eligible completed responses, validates output, and degrades with stable
  unavailable behavior. [PRD 6.12]
- FR-73: Speech expires from generation under configured retention, is reused only for matching source and voice, and
  retries only on explicit request. [PRD 6.12]
- FR-74: Only shared-course supervisors may ban student-only accounts; bans affect future requests but not in-flight work
  and must be removed before staff grant. [PRD 6.13]
- FR-75: Operational data persists until authorized deletion except generated speech, and MIA has no data export.
  [PRD 6.14]
- FR-76: Authorized administrators delete student-only or staff accounts under last-admin and sole-supervisor guards,
  atomically applying all cleanup and triage. [PRD 6.14]
- FR-77: Student deletion removes all live operational data and retains only random de-identified content-free audit;
  provider copies and backups remain operator-controlled. [PRD 6.14]
- FR-78: Destructive operations do not coordinate with workers; guarded late commits discard results and never restore,
  upsert, requeue, or retry deleted targets. [PRD 6.14]
- FR-79: Administrators access bounded, stable, content-free audit events for required security, mutation, provider, job,
  invitation, correction, and operator actions. [PRD 6.15]
- FR-80: Audit content excludes every password, code, secret, token, sensitive body, and forbidden personal value.
  [PRD 6.15]
- FR-81: API instants are UTC RFC 3339 `Z`; display uses preferred IANA zones, and DST-stable schedules separate local
  time and zone. [PRD 6.16]
- FR-82: Release one sends no workflow notifications; account and security deliveries are not workflow notifications.
  [PRD 6.17]
- FR-83: Administrators inspect jobs while supervisors receive safe scoped processing state; retry remains domain-specific.
  [PRD 6.18]
- FR-84: Background jobs use at most three bounded transient retries with current code/config and guarded durable commits;
  usage is operational, not billing. [PRD 6.18]

### Non-Functional Requirements

- NFR-1: Explicit action- and scope-aware authorization protects every sensitive resource, field, download, and retrieval.
  [PRD 7.1]
- NFR-2: Public and out-of-scope responses hide existence, including indistinguishable password-recovery outcomes.
  [PRD 7.1]
- NFR-3: Files and all external, user, OCR, and model content are untrusted, bounded, validated, non-executable, and
  non-authoritative. [PRD 7.1]
- NFR-4: Passwords use fixed Argon2id, bearer tokens use SHA-256 digests, and secrets use redacted file or environment
  configuration only. [PRD 7.1]
- NFR-5: MVP fields have no application encryption; private data-directory and backup access protect plaintext TOTP and
  active SMS secrets. [PRD 7.1]
- NFR-6: Logs and audits exclude credentials, cookies, MFA data, prompts, message bodies, provider payloads, and needless
  personal data. [PRD 7.1]
- NFR-7: Client IP resolution trusts only configured proxies, loopback, or the controlled Unix listener and uses bounded
  right-to-left parsing with safe fallback. [PRD 7.1]
- NFR-8: Reverse-proxy HTTPS and hardened GET/HEAD static serving are mandatory, with strict CSP and configured-origin
  security links. [PRD 7.1]
- NFR-9: Every public API route is rate limited, and authentication-sensitive routes combine IP and identifier dimensions
  without disclosure. [PRD 7.2]
- NFR-10: Login, recovery, invitation, reset, MFA, and mobile verification enforce the fixed windows, delays, and limits.
  [PRD 7.2]
- NFR-11: Process limiter state is a 50,000-key expiring LRU, SMS limits are durable, and no separate listed authenticated
  feature limits exist. [PRD 7.2]
- NFR-12: Tutor and summarization token, chunk, and reduction budgets are fixed; oversized summary input fails visibly.
  [PRD 7.3]
- NFR-13: Every input class is bounded, and non-upload request bodies are capped at 1 MiB. [PRD 7.3]
- NFR-14: SQLite and files are one backup unit; WAL/FULL durability and atomic file publication preserve consistency.
  [PRD 7.4]
- NFR-15: Startup performs ordered cleanup and stranded-work failure, fails on missing required files, and removes orphans.
  [PRD 7.4]
- NFR-16: Tutor output persists at least every 16 KiB or second and before terminal commit. [PRD 7.4]
- NFR-17: MIA is single-node without HA; provider outages never block startup and visibly affect only dependent features.
  [PRD 7.5]
- NFR-18: Request-path provider operations use fixed deadlines and no automatic retry after ambiguous failure. [PRD 7.5]
- NFR-19: One executable provides all three commands; every database user holds the exclusive data lock, and offline
  commands avoid providers. [PRD 7.6]
- NFR-20: Startup strictly validates configuration and applies forward-only migrations; configuration changes require
  restart. [PRD 7.6]
- NFR-21: Automated tests never call paid or production providers. [PRD 7.6]

### Additional Requirements

- AR-01: Organize implementation as vertical feature packages over a thin shared kernel; add packages only with their
  feature. [ARCH AD-1]
- AR-02: Keep feature imports acyclic and directed toward the kernel; the kernel never imports features. [ARCH AD-2]
- AR-03: Give every table one owner and perform cross-feature operations through owner APIs in one initiating transaction.
  [ARCH AD-3]
- AR-04: Fetch resources through action- and scope-aware queries that return identical missing and out-of-scope results.
  [ARCH AD-4]
- AR-05: Use exactly the durable jobs, tutor-response manager, and speech-cache execution systems for their assigned work.
  [ARCH AD-5]
- AR-06: Commit asynchronous results only through state or lease guards and discard zero-row results without resurrection.
  [ARCH AD-6]
- AR-07: Maintain one stable error registry and one HTTP mapping layer; classify provider retryability at adapters.
  [ARCH AD-7]
- AR-08: Use one encrypted stateless session, mandatory per-request identity reload, declared stages, and controlled
  security-generation changes. [ARCH AD-8]
- AR-09: Use locked `modernc.org/sqlite` with fixed path and pragmas, four connections, and embedded forward-only
  migrations. [ARCH AD-9]
- AR-10: Provider adapters allowlist fields, own fixed deadlines, avoid ambiguous request retries, and share durable SMS
  limiting. [ARCH AD-10]
- AR-11: Publish files atomically, make database records unreachable before deletion, and enforce ordered startup
  integrity checks. [ARCH AD-11]
- AR-12: Write bounded content-free audit events in the same transaction as the mutation, with audit-owned deletion
  fingerprints. [ARCH AD-12]
- AR-13: The user package owns accounts, roles, and security state and exposes the single creation and role-grant APIs.
  [ARCH AD-13]
- AR-14: Preserve canonical feature import direction and perform account/course cascades through the lifecycle registry.
  [ARCH AD-14]
- AR-15: Ship one `mia` executable with `serve`, `bootstrap-admin`, and `reset-admin-mfa`; database commands lock before
  opening SQLite. [ARCH Executable]
- AR-16: Use prefixed opaque UUID v4 IDs, strict UTC instants, normalized uniqueness keys, parameterized SQL, and checked
  enums and JSON. [ARCH Conventions]
- AR-17: Centralize identity and text normalization while leaving password and chat bytes unchanged. [ARCH Conventions]
- AR-18: Pass context through request, database, job, and provider paths; avoid mutable globals and close owned resources.
  [ARCH Conventions]
- AR-19: Centralize structured logging and redaction, use stable naming registries, and expose no MVP health or metrics
  endpoint. [ARCH Conventions]
- AR-20: Centralize named bounded rate limiters, trusted-proxy client identity, pagination, links, and request-ID
  idempotency. [ARCH Conventions]
- AR-21: Serve a separately installed SPA securely outside `/api`, with strict static-file controls and startup-derived
  cookie policy. [ARCH HTTP Runtime]
- AR-22: Enforce fixed HTTP, SSE, provider, shutdown, tutor-token, retrieval-round, excerpt, and persistence deadlines and
  bounds. [ARCH Runtime and Providers]
- AR-23: Build provider contracts against current APIs before implementation and validate all provider settings locally
  without startup calls. [ARCH Implementation Prerequisites]
- AR-24: Use real embedded migrations and local fakes in tests, never paid providers, and run formatting, test, vet, lint,
  race, API, duplication, vulnerability, and documentation gates. [ARCH Verification]

### UX Design Requirements

- UX-DR-001: Provide same-origin session discovery from a no-cookie state and issue or refresh public CSRF state.
  [BCG-001]
- UX-DR-002: Represent anonymous, MFA, password-change, and authenticated stages authoritatively. [BCG-001]
- UX-DR-003: Return the MFA challenge ID and relevant restricted, idle, and absolute UTC expiry instants. [BCG-001]
- UX-DR-004: Define CSRF rotation and stale or invalid cookie clearing for every authentication transition. [BCG-001]
- UX-DR-005: Prevent any response authorized before confirmed logout from reinstalling authenticated cookie state.
  [BCG-001]
- UX-DR-006: Support non-refreshing ordered session, capability, scope, resource, work-state, and SSE checks. [BCG-001]
- UX-DR-007: Provide one Continue working operation returning refreshed idle and unchanged absolute deadlines. [BCG-001]
- UX-DR-008: Define explicit SSE reconnect that does not refresh idle and its relationship to Continue working. [BCG-001]
- UX-DR-009: Return unchanged authoritative deadlines after every non-refreshing check. [BCG-001]
- UX-DR-010: Specify bounded JSON:API responses, security, public limiting, no-store, and minimal stage data. [BCG-001]
- UX-DR-011: Return the current student's active tutoring session across all courses or an unambiguous none result.
  [BCG-002]
- UX-DR-012: Make active-session discovery atomic with the one-active-session invariant. [BCG-002]
- UX-DR-013: Return enough session and course identity for authorized navigation and retrieval. [BCG-002]
- UX-DR-014: Define success, none, sign-in-expired, and existence-safe unavailable outcomes. [BCG-002]
- UX-DR-015: Use the no-store discovery operation to reconcile ambiguous creation and active-session conflicts. [BCG-002]
- UX-DR-016: Return server-authored current-account capabilities with global, course, and student scopes. [BCG-003]
- UX-DR-017: Make capability data sufficient for shell visibility and valid entry scopes without policy reconstruction.
  [BCG-003]
- UX-DR-018: Represent no capabilities and empty scope explicitly and reflect relevant changes on the next check.
  [BCG-003]
- UX-DR-019: Support non-refreshing capability and scope checks for explicit recovery actions. [BCG-003]
- UX-DR-020: Keep operation authorization independent and define no-store authenticated errors. [BCG-003]
- UX-DR-021: Represent queued, generating, retry-scheduled, generated, corrected, failed, and unavailable summary states.
  [BCG-004]
- UX-DR-022: Return server-authored summary-regeneration eligibility independently from lifecycle state. [BCG-004]
- UX-DR-023: Return only authorized lifecycle, last-checked, state-change, and retry timing. [BCG-004]
- UX-DR-024: Preserve summary source and attribution and keep transcript availability independent. [BCG-004]
- UX-DR-025: Define student and assigned-supervisor visibility, existence-safe errors, and no-store. [BCG-004]
- UX-DR-026: Apply `Cache-Control: no-store` to every named sensitive JSON, SSE, binary, speech, and one-time response.
  [BCG-005]
- UX-DR-027: Decide whether narrowly non-sensitive authenticated images may use a different policy. [BCG-005]
- UX-DR-028: Define separate inline and download policies for avatars, logos, and mentor-scoped avatars. [BCG-005]
- UX-DR-029: Fully specify revalidation, validators, `304`, proxy, revocation, and cross-account safety for image exceptions.
  [BCG-005]
- UX-DR-030: Give sensitive-route errors equivalent no-store protection while retaining per-request authorization.
  [BCG-005]
- UX-DR-031: Prohibit reverse-proxy storage and replay of protected API and SSE responses. [BCG-005]
- UX-DR-032: Represent a pending usable invitation whose latest delivery outcome is ambiguous. [BCG-006]
- UX-DR-033: Expose the required sanitized pending failure code and authoritative attempt timing. [BCG-006]
- UX-DR-034: Distinguish confirmed admission from timeout for create and resend directly or by documented reconciliation.
  [BCG-006]
- UX-DR-035: Reconcile definite resend failure as faulty with an unusable token. [BCG-006]
- UX-DR-036: Preserve no automatic retry and previous-token invalidation after resend. [BCG-006]
- UX-DR-037: Set one fixed aggregate byte ceiling for every application and reverse-proxy API error body. [BCG-007]
- UX-DR-038: Require valid UTF-8 and explicit media type and define complete browser-visible decoded text. [BCG-007]
- UX-DR-039: Require neutral existence-hiding errors without secrets, personal data, internals, prompts, bodies, or provider
  payloads. [BCG-007]
- UX-DR-040: Define bounded safe intermediary behavior for unavailable or invalid upstream responses. [BCG-007]
- UX-DR-041: Apply the BCG-005 no-store policy to every error response. [BCG-007]
- UX-DR-042: Provide an administrator-authorized paginated account collection with bounded filters and deterministic order.
  [BCG-008]
- UX-DR-043: Provide administrator account detail by the opaque user ID used for mutations. [BCG-008]
- UX-DR-044: Return only authorized global metadata plus server-authored target state and action eligibility. [BCG-008]
- UX-DR-045: Define empty, stale, deleted, expired, and existence-safe no-store outcomes. [BCG-008]
- UX-DR-046: Read no factor, active factor, and pending enrollment or replacement with required opaque IDs. [BCG-009]
- UX-DR-047: Return method, pending expiry, replacement relation, and eligibility for every MFA action. [BCG-009]
- UX-DR-048: Return at most a reviewed masked SMS destination and never return factor or proof secrets. [BCG-009]
- UX-DR-049: Define invalidation and availability outcomes with no-store and authoritative mutation reconciliation.
  [BCG-009]
- UX-DR-050: Provide authenticated student-only and staff MFA-reset operations under their distinct scopes. [BCG-010]
- UX-DR-051: Enforce shared-course supervisor and different-administrator rules and expose no sole-admin HTTP route.
  [BCG-010]
- UX-DR-052: Atomically remove factors, codes, challenges, and proofs and apply exact cookie, password, and audit effects.
  [BCG-010]
- UX-DR-053: Define definitive, hidden, stale, unavailable, and ambiguous reconciliation without returning secrets.
  [BCG-010]
- UX-DR-054: Represent inactive-incomplete, inactive-activatable, active-accepting, and active-not-accepting course states.
  [BCG-011]
- UX-DR-055: Return viewer-safe prerequisite blockers with authorized resolution links. [BCG-011]
- UX-DR-056: Return activation and session-start eligibility separately from course state and material counts. [BCG-011]
- UX-DR-057: Recompute readiness atomically after every dependency change and define no-store unavailable outcomes.
  [BCG-011]
- UX-DR-058: Represent idle, generating response, queued message and response, and remaining queue capacity atomically.
  [BCG-012]
- UX-DR-059: Return eligibility for submit, queue, interrupt, retry, and finish. [BCG-012]
- UX-DR-060: Preserve identities through terminal-to-queued automatic handoff. [BCG-012]
- UX-DR-061: Return equivalent current work after mutations or define reconciliation for every outcome class. [BCG-012]
- UX-DR-062: Reconcile Stop against both its immutable target response and current work, with no-store terminal reads.
  [BCG-012]
- UX-DR-063: Return current-student mentoring-request eligibility as checking, allowed, disabled, lost, or unavailable.
  [BCG-013]
- UX-DR-064: Atomically evaluate course activity, student permission, and qualifying mentor assignment. [BCG-013]
- UX-DR-065: Keep existing mentoring records usable when creation is disabled. [BCG-013]
- UX-DR-066: Disclose only viewer-safe disable reasons and refresh them after dependencies change, using no-store.
  [BCG-013]
- UX-DR-067: Bind every inventoried consequential mutation to expected state/version or a meaning-stable operation.
  [BCG-014]
- UX-DR-068: Make confirmed invitation revoke incapable of deletion and confirmed faulty deletion incapable of revocation.
  [BCG-014]
- UX-DR-069: Reject stale preconditions without mutation and require fresh read, review, and confirmation. [BCG-014]
- UX-DR-070: Return definitive committed-effect semantics while preserving authorization, hiding, CSRF, audit, and no-store.
  [BCG-014]
- UX-DR-071: Read mobile-verification as active, expired, invalidated, completed, absent, or unavailable after reload.
  [BCG-015]
- UX-DR-072: Return authoritative expiry, resend eligibility, and next-resend instant. [BCG-015]
- UX-DR-073: Resend retains expiry and must define whether the hidden failure count resets or carries forward. [BCG-015]
- UX-DR-074: Return equivalent lifecycle after create, resend, verify, and concurrent invalidation. [BCG-015]
- UX-DR-075: Distinguish cooldown from hourly/daily throttle without exposing thresholds, destination, code, count, or keys.
  [BCG-015]
- UX-DR-076: Return mentor completion as allowed, not allowed, access lost, terminal, or unavailable. [BCG-016]
- UX-DR-077: When schedule time blocks completion, return the UTC instant for an explicit eligibility recheck. [BCG-016]
- UX-DR-078: Recompute before confirmation and define success, stale denial, ambiguity reconciliation, and no-store.
  [BCG-016]
- UX-DR-079: Resolve one exact user ID as an eligible registered-staff mentor target through explicit bounded transport.
  [BCG-017]
- UX-DR-080: Allow no partial, fuzzy, batch, browse, autocomplete, suggestion, pagination, or result-count behavior.
  [BCG-017]
- UX-DR-081: Return only minimum wrong-person-prevention identity and server-authored mentor-grant action state.
  [BCG-017]
- UX-DR-082: Make every hidden or throttled target cause one fixed unavailable outcome without identifier-specific timing.
  [BCG-017]
- UX-DR-083: Recheck and atomically bind the idempotent grant, reconcile without replay, use no-store, and leave unrelated
  work available. [BCG-017]

### Resolved Product And Transport Decisions

- Only the explicit Continue working operation refreshes idle expiry; authenticated reads, other mutations, and SSE do
  not. [BCG-001]
- An independent signed browser marker makes a pre-logout response's late authentication cookie unusable. [BCG-001]
- Every API response uses `Cache-Control: no-store`; authenticated images have no exception. [BCG-005]
- API errors are valid UTF-8 JSON:API no larger than 64 KiB, with unsafe or oversized detail replaced generically.
  [BCG-007]
- Consequential state-dependent mutations use strong `ETag` and `If-Match` preconditions. [BCG-014]
- Mobile-code resend preserves the existing code, expiry, and failure count. [BCG-015]
- Mentor-target resolution is a focused POST preflight returning only ID, username, nullable display name, and action
  state. Hidden and throttled targets use one fixed unavailable outcome. [BCG-017]

### Requirements Coverage Map

- FR-1: Epic 1 - Username validation and uniqueness.
- FR-2: Epic 1 - Email validation and uniqueness.
- FR-3: Epic 1 baseline; Epic 2 - Browser-visible account state and capabilities.
- FR-4: Epic 1 - Optional profile-field validation.
- FR-5: Epic 1 - Descriptive-text normalization.
- FR-6: Epic 1 - Closed registration model.
- FR-7: Epic 1 - Offline administrator bootstrap.
- FR-8: Epic 1 - Invitation-based staff registration.
- FR-9: Epic 1 baseline; Epic 2 - Ambiguous invitation-delivery state.
- FR-10: Epic 1 baseline; Epic 2 - Safe invitation reconciliation and intent binding.
- FR-11: Epic 1 - Existence-hiding invitation preview.
- FR-12: Epic 1 - Atomic invitation acceptance.
- FR-13: Epic 1 baseline; Epic 2 - Privacy-safe direct mentor-role targeting and grant state.
- FR-14: Epic 1 - Bearer-token format and storage.
- FR-15: Epic 1 - Student provisioning.
- FR-16: Epic 1 - Existing-student course membership.
- FR-17: Epic 1 - Student rejoining after removal.
- FR-18: Epic 1 - Password policy.
- FR-19: Epic 1 - Student password recovery.
- FR-20: Epic 1 - Staff password recovery.
- FR-21: Epic 1 - Recovery delivery outcomes.
- FR-22: Epic 1 baseline; Epic 2 - Non-refreshing checks and explicit idle refresh.
- FR-23: Epic 1 baseline; Epic 2 - Current account and authorization discovery.
- FR-24: Epic 1 baseline; Epic 2 - Authoritative restricted-session discovery.
- FR-25: Epic 1 baseline; Epic 2 - Late-response-safe logout semantics.
- FR-26: Epic 1 baseline; Epic 2 - Current MFA-factor discovery.
- FR-27: Epic 1 baseline; Epic 2 - MFA method and destination state.
- FR-28: Epic 1 baseline; Epic 2 - MFA lifecycle and resend observability.
- FR-29: Epic 1 baseline; Epic 2 - Recovery-code lifecycle visibility.
- FR-30: Epic 1 baseline; Epic 2 - MFA action eligibility and atomic preconditions.
- FR-31: Epic 1 baseline; Epic 2 - MFA invalidation reconciliation.
- FR-32: Epic 1 baseline; Epic 2 - Authorized student and staff MFA reset.
- FR-33: Epic 1 baseline; Epic 2 - Staff profile capabilities.
- FR-34: Epic 1 baseline; Epic 2 - Student profile management capabilities.
- FR-35: Epic 1 baseline; Epic 2 - Mobile-verification lifecycle and resend state.
- FR-36: Epic 1 - Avatar and logo processing.
- FR-37: Epic 1 baseline; Epic 2 - Course-state discovery.
- FR-38: Epic 1 baseline; Epic 2 - Course-management capabilities and intent binding.
- FR-39: Epic 1 baseline; Epic 2 - Course activation and session-start readiness.
- FR-40: Epic 1 baseline; Epic 2 - Active-course acceptance state.
- FR-41: Epic 1 baseline; Epic 2 - Course lifecycle eligibility and intent binding.
- FR-42: Epic 1 baseline; Epic 2 - Student-removal eligibility and intent binding.
- FR-43: Epic 1 baseline; Epic 2 - Course-deletion eligibility and intent binding.
- FR-44: Epic 1 baseline; Epic 2 - Supervisor tutoring-entry scope.
- FR-45: Epic 1 - Material validation and identity.
- FR-46: Epic 1 - Course-material approval and revocation.
- FR-47: Epic 1 - Private-material access.
- FR-48: Epic 1 - Material finalization and repair.
- FR-49: Epic 1 - Structured material briefs.
- FR-50: Epic 1 - Material processing bounds.
- FR-51: Epic 1 - External-link material.
- FR-52: Epic 1 - Private-material deletion.
- FR-53: Epic 1 - Authorized material downloads.
- FR-54: Epic 1 baseline; Epic 2 - Cross-course active-session discovery and start eligibility.
- FR-55: Epic 1 baseline; Epic 2 - Tutoring work and finish eligibility.
- FR-56: Epic 1 baseline; Epic 2 - Ambiguous tutoring-mutation reconciliation.
- FR-57: Epic 1 - Tutor context construction.
- FR-58: Epic 1 - Tutor token budgets.
- FR-59: Epic 1 - Authorized material retrieval.
- FR-60: Epic 1 baseline; Epic 2 - Current tutoring-work state and action eligibility.
- FR-61: Epic 1 baseline; Epic 2 - Stop, reconnect, and failure reconciliation.
- FR-62: Epic 1 baseline; Epic 2 - Retry eligibility and intent binding.
- FR-63: Epic 1 baseline; Epic 2 - Summary lifecycle and regeneration eligibility.
- FR-64: Epic 1 baseline; Epic 2 - Queued and stranded work observability.
- FR-65: Epic 1 baseline; Epic 2 - Mentoring-request entry state.
- FR-66: Epic 1 baseline; Epic 2 - Student mentoring-request eligibility.
- FR-67: Epic 1 baseline; Epic 2 - Mentor-assignment capabilities.
- FR-68: Epic 1 baseline; Epic 2 - Mentoring work and action state.
- FR-69: Epic 1 baseline; Epic 2 - Authoritative mentor-completion eligibility.
- FR-70: Epic 1 baseline; Epic 2 - Safe mentor reassignment and removal intent.
- FR-71: Epic 1 baseline; Epic 2 - Existing-work access versus request creation.
- FR-72: Epic 1 - Generated speech.
- FR-73: Epic 1 - Speech retention and retry.
- FR-74: Epic 1 baseline; Epic 2 - Ban eligibility and intent binding.
- FR-75: Epic 1 - Operational retention.
- FR-76: Epic 1 baseline; Epic 2 - Account-deletion eligibility and intent binding.
- FR-77: Epic 1 - Student-deletion privacy.
- FR-78: Epic 1 - Safe late asynchronous commits.
- FR-79: Epic 1 baseline; Epic 2 - Administrator account targeting and safe action state.
- FR-80: Epic 1 - Audit-content exclusions.
- FR-81: Epic 1 - UTC and local-time representation.
- FR-82: Epic 1 - Release-one notification scope.
- FR-83: Epic 1 - Job inspection and scoped processing state.
- FR-84: Epic 1 - Background-job retry and commit behavior.

#### UX Design Requirements Coverage

- UX-DR-001 through UX-DR-010: Stories 2.1 and 2.2 - Browser session and CSRF lifecycle.
- UX-DR-011 through UX-DR-015: Story 2.8 - Cross-course active-session discovery.
- UX-DR-016 through UX-DR-020: Story 2.4 - Current-account capabilities and scope.
- UX-DR-021 through UX-DR-025: Story 2.10 - Tutoring-summary lifecycle.
- UX-DR-026 through UX-DR-031: Story 2.1 - Protected-response storage policy.
- UX-DR-032 through UX-DR-036: Story 2.3 - Invitation delivery state.
- UX-DR-037 through UX-DR-041: Story 2.1 - Bounded safe errors.
- UX-DR-042 through UX-DR-045: Story 2.4 - Account-administration reads.
- UX-DR-046 through UX-DR-049: Story 2.5 - MFA-factor discovery.
- UX-DR-050 through UX-DR-053: Story 2.5 - Authorized MFA reset.
- UX-DR-054 through UX-DR-057: Story 2.7 - Course readiness.
- UX-DR-058 through UX-DR-062: Story 2.9 - Tutoring work and actions.
- UX-DR-063 through UX-DR-066: Story 2.11 - Mentoring-request eligibility.
- UX-DR-067 through UX-DR-070: Stories 2.3, 2.4, 2.7, 2.11, and 2.12 - Atomic intent binding.
- UX-DR-071 through UX-DR-075: Story 2.6 - Mobile-verification lifecycle.
- UX-DR-076 through UX-DR-078: Story 2.11 - Mentor-completion eligibility.
- UX-DR-079 through UX-DR-083: Story 2.12 - Supervisor-safe mentor-role targeting.

## Epic List

### Epic 1: Complete MIA Backend Foundation

Users and staff can perform the confirmed MIA account, course, material, tutoring, mentoring, speech, and administration
workflows through the existing backend.

**FRs covered:** FR-1 through FR-84

**Status:** Complete

### Epic 2 Summary: Reliable Browser-Ready Workflows

Students, mentors, supervisors, and administrators can safely discover current state, understand which actions are
available, and complete existing workflows through authoritative, privacy-safe, race-safe, and recoverable browser
contracts.

**FRs extended:** FR-3, FR-9, FR-10, FR-13, FR-22 through FR-35, FR-37 through FR-44, FR-54 through FR-56, FR-60
through FR-71, FR-74, FR-76, and FR-79

**Additional coverage:** UX-DR-001 through UX-DR-083, including BCG-001 through BCG-017

Epic 2 builds on the completed Epic 1. It resolves the seven recorded product and transport decisions, updates OpenAPI and
human-readable contracts with implementation, and delivers the complete browser-consumable contract without depending on
a future epic.

## Epic 2: Reliable Browser-Ready Workflows

Students, mentors, supervisors, and administrators can safely discover current state, understand which actions are
available, and complete existing workflows through authoritative, privacy-safe, race-safe, and recoverable browser
contracts.

### Story 2.1: Protect Browser Responses and Bound Error Disclosure

As a MIA browser user,
I want protected responses and failures to resist storage and unsafe disclosure,
So that shared devices and intermediaries do not retain sensitive data while the interface receives complete safe errors.

**Requirements:** NFR-1, NFR-2, NFR-6, NFR-8, NFR-13; AR-07, AR-19, AR-21; UX-DR-010, UX-DR-026 through UX-DR-031,
UX-DR-037 through UX-DR-041

**Acceptance Criteria:**

**Given** any public or authenticated API response, including JSON, SSE, binary, bodyless, and error responses
**When** MIA returns the response
**Then** it includes `Cache-Control: no-store`
**And** no protected response type receives an authenticated-image caching exception.

**Given** an API request produces any success or error containing protected or one-time data
**When** the application or reverse proxy sends the response
**Then** intermediaries are instructed not to store or replay it
**And** the documented reverse-proxy configuration preserves or strengthens this policy.

**Given** MIA returns an API error body
**When** the browser decodes the complete response
**Then** it is valid UTF-8 JSON:API with the documented media type and no more than 65,536 bytes
**And** the response is never silently truncated into invalid or incomplete JSON.

**Given** an error detail would exceed the ceiling or contains unsafe dependency content
**When** MIA maps the error
**Then** it returns a bounded stable generic registry error instead of forwarding or truncating the unsafe detail
**And** status, stable code, and existence-hiding behavior remain correct.

**Given** any application or intermediary-generated API error
**When** its body is constructed
**Then** it excludes secrets, credentials, cookies, personal data, internal paths, prompts, message bodies, and provider
payloads
**And** it includes `Cache-Control: no-store`.

**Given** the OpenAPI conformance and HTTP test suites
**When** response-policy tests run
**Then** they verify caching headers across every protected representation and error class
**And** they verify the 64 KiB ceiling, valid JSON:API encoding, generic replacement, and prohibited-content boundaries.

### Story 2.2: Discover and Extend Browser Sessions Safely

As a MIA browser user,
I want authoritative session state with deliberate idle extension and reliable logout,
So that the interface can enforce each authentication stage without background activity keeping me signed in or stale
responses restoring access.

**Requirements:** FR-22 through FR-25; NFR-1, NFR-2, NFR-8 through NFR-10; AR-08, AR-20 through AR-22; UX-DR-001
through UX-DR-010

**Acceptance Criteria:**

**Given** a browser has no valid authentication cookie
**When** it performs same-origin session discovery
**Then** MIA returns the authoritative anonymous stage without revealing account existence
**And** it issues or refreshes the public CSRF state required for the next authentication transition.

**Given** a valid browser session is in `mfa`, `password-change`, or `authenticated` stage
**When** session discovery succeeds
**Then** the response identifies the exact stage and returns its applicable server-authored UTC expiry instants
**And** MFA state includes the opaque challenge ID without exposing factor secrets or destinations.

**Given** any authenticated read, mutation, SSE event, reconnect, or lifecycle check other than Continue working
**When** MIA processes it successfully
**Then** the idle and absolute deadlines remain unchanged
**And** every non-refreshing authenticated response returns both authoritative unchanged deadlines.

**Given** an authenticated user deliberately invokes the CSRF-protected Continue working operation before expiry
**When** MIA accepts it
**Then** only the idle deadline advances, the absolute deadline remains unchanged, and the rotated cookie is returned
**And** the response contains the new idle deadline and unchanged absolute deadline.

**Given** Continue working succeeds while the client needs an SSE replacement
**When** the refreshed response is received
**Then** the Continue working response does not open or implicitly reconnect SSE
**And** the client may explicitly open the replacement EventSource afterward without another idle refresh.

**Given** a login, restricted-stage transition, password replacement, or logout succeeds
**When** MIA changes authentication state
**Then** it rotates the required session and CSRF values according to the documented transition
**And** stale or invalid cookies are cleared without exposing why authentication failed.

**Given** a browser contains an authenticated cookie issued by a response authorized before a confirmed logout
**When** that response arrives after logout or the cookie is used afterward
**Then** an independent signed browser generation marker makes the old authenticated cookie unusable
**And** the next session discovery returns anonymous state and clears stale authentication without invalidating other
browser sessions.

**Given** an SSE connection or reconnect
**When** MIA authorizes and serves it
**Then** neither connection establishment nor traffic refreshes the idle deadline
**And** an expired sign-in terminates protected access using the same authoritative session rules.

**Given** concurrent session, transition, Continue working, and logout requests
**When** HTTP integration tests exercise response reordering
**Then** no pre-logout response can restore usable authenticated state
**And** idle, absolute, CSRF, restricted-stage, account-state, rate-limit, and `no-store` behavior matches OpenAPI and the
human-readable contracts.

### Story 2.3: Manage Invitations Through Delivery Ambiguity

As an authorized invitation manager,
I want authoritative delivery state and mutations bound to the invitation I reviewed,
So that I can respond safely to SMTP uncertainty without revoking, deleting, or resending the wrong state.

**Requirements:** FR-9, FR-10, FR-14; NFR-1, NFR-2, NFR-6, NFR-9, NFR-10; AR-03, AR-04, AR-07, AR-10, AR-12,
AR-20; UX-DR-032 through UX-DR-036, UX-DR-067 through UX-DR-070

**Acceptance Criteria:**

**Given** invitation creation or resend reaches an SMTP timeout after MIA admits the operation
**When** the invitation is read or the ambiguous result is reconciled
**Then** it remains pending and usable with a stable documented ambiguous-delivery code
**And** the response includes authoritative attempt timing without claiming delivery or retrying automatically.

**Given** initial invitation delivery fails definitively
**When** MIA commits the result
**Then** the invitation becomes faulty and its bearer token is unusable
**And** authorized reads expose only the sanitized documented failure state.

**Given** resend is accepted
**When** MIA rotates the bearer token and attempts delivery
**Then** the previous token is permanently invalidated
**And** definite failure makes the invitation faulty while timeout leaves the new token pending and usable.

**Given** an authorized manager reads a manageable invitation
**When** MIA returns its current representation
**Then** the response includes a strong `ETag` covering every state that can change the reviewed mutation's effect
**And** pending, ambiguous pending, faulty, accepted, and revoked states remain distinguishable only to authorized managers.

**Given** a manager confirms revocation of a pending invitation or deletion of a faulty invitation
**When** the client submits the mutation with the reviewed `If-Match` value
**Then** MIA atomically verifies authorization, current state, and the precondition before applying exactly the confirmed
effect
**And** success identifies whether the invitation was retained as revoked or physically deleted.

**Given** `If-Match` is absent or the invitation changed after review
**When** the manager submits the mutation
**Then** MIA returns the documented precondition-required or stale-precondition error without changing the invitation
**And** the client must reread and obtain fresh confirmation rather than replaying the mutation.

**Given** an invitation is accepted, revoked, out of scope, missing, or concurrently transitions during a mutation
**When** the operation is evaluated
**Then** authorization and existence-hiding rules remain intact
**And** no response reveals hidden invitation state or bearer-token material.

**Given** invitation contract and concurrency tests
**When** pending invitations transition to faulty, accepted, or revoked between read and mutation
**Then** no confirmed revoke can become physical deletion and no confirmed faulty deletion can become revocation
**And** create, resend, reconciliation, `ETag`, `If-Match`, audit, CSRF, rate-limit, and `no-store` behavior matches OpenAPI.

### Story 2.4: Discover Account Capabilities and Administration Targets

As an authenticated MIA user,
I want authoritative capabilities and appropriately scoped account administration reads,
So that I see only valid workflows and can administer accounts without reconstructing authorization policy.

**Requirements:** FR-3, FR-13, FR-23, FR-33, FR-34, FR-37, FR-38, FR-41 through FR-44, FR-74, FR-76, FR-79; NFR-1,
NFR-2, NFR-6; AR-03, AR-04, AR-07, AR-12, AR-20; UX-DR-016 through UX-DR-020, UX-DR-042 through UX-DR-045,
UX-DR-067 through UX-DR-070

**Acceptance Criteria:**

**Given** an authenticated account with any supported combination of roles and assignments
**When** current-account capabilities are read
**Then** MIA returns stable release-one action identifiers with explicit global, course, student, or own-resource scopes
**And** multiple roles produce only the valid union without widening course, student, ownership, or action scope.

**Given** an account has no capability or no valid scope for one
**When** MIA returns the current-account representation
**Then** the absence is represented explicitly rather than inferred from omitted collections or failed operations
**And** the representation is sufficient for shell visibility and valid entry-scope discovery.

**Given** roles, assignments, membership, ban state, or account state change
**When** the client explicitly rechecks capabilities
**Then** the next response reflects the committed state
**And** the check does not refresh the authentication idle deadline or replace operation-level authorization.

**Given** an administrator requests the account collection
**When** supported pagination and filters are valid
**Then** MIA returns bounded minimal global metadata ordered by normalized username and opaque ID
**And** filters support exact normalized username, account class, permanent role, and account state.

**Given** no accounts match an authorized collection request
**When** MIA returns the page
**Then** it returns a valid empty collection with deterministic pagination metadata
**And** it does not substitute a not-found or unavailable error.

**Given** an unsupported, malformed, or excessive account filter or page request
**When** MIA validates the collection request
**Then** it returns the documented bounded-input error
**And** it performs no broad text search, fuzzy matching, or private course-data aggregation.

**Given** an administrator requests account detail by opaque user ID
**When** the target is in scope
**Then** MIA returns only ID, username, account class and state, permanent roles, server-authored action eligibility, and
consequence summaries
**And** it excludes email and all other profile, course-content, tutoring, mentoring, and security fields.

**Given** account state, roles, administrator count, or course-supervisor responsibility affects a supported mutation
**When** MIA returns account detail
**Then** it includes a strong `ETag` covering every value that can change the reviewed effect
**And** it reports eligibility and viewer-safe blockers for permanent role grant and account deletion.

**Given** an administrator submits permanent role grant or account deletion
**When** the request carries the reviewed `If-Match` value
**Then** MIA atomically rechecks authorization, account class and state, last-administrator and sole-supervisor guards, and
the precondition before committing
**And** it applies exactly the documented role coupling, cleanup, triage, audit, and idempotent success semantics.

**Given** the precondition is absent or stale, the target disappeared, or administrator scope was lost
**When** an account mutation or read is attempted
**Then** MIA performs no unintended mutation and returns the documented precondition or existence-safe unavailable outcome
**And** protected account data is not retained in a response after access loss.

**Given** the administrator's sign-in expires during an account collection, detail, or mutation request
**When** MIA evaluates the request
**Then** it returns the documented sign-in-expired response without account data or mutation
**And** the response remains distinguishable from an authorized empty collection.

**Given** capability and administration contract tests
**When** representative single-role, multi-role, assignment, ownership, account-state, and concurrent-mutation cases run
**Then** they prove capability accuracy, list bounds, deterministic navigation, minimal field disclosure, stale no-op
behavior, and independent operation authorization
**And** OpenAPI documents every action identifier, scope shape, filter, field, error, precondition, and `no-store` response.

### Story 2.5: Inspect MFA State and Recover Lost Factors

As a MIA account holder or authorized recovery actor,
I want authoritative MFA state and the correct lost-factor recovery operation,
So that enrollment, replacement, disablement, and recovery remain usable after reload without exposing secrets.

**Requirements:** FR-26 through FR-32; NFR-1, NFR-2, NFR-6, NFR-10; AR-03, AR-04, AR-08, AR-12, AR-20;
UX-DR-046 through UX-DR-053

**Acceptance Criteria:**

**Given** an authenticated account has no factor, an active TOTP or SMS factor, a pending first enrollment, or a pending
replacement
**When** the account holder reads MFA state
**Then** MIA explicitly represents the active and pending states with the opaque IDs required by supported mutations
**And** it returns method, pending expiry, replacement relationship, and separate eligibility for enroll, continue, cancel,
disable, replace, verify, and SMS resend.

**Given** MFA state is returned after initial setup
**When** the response is constructed
**Then** it excludes TOTP secrets, provisioning data, codes, recovery codes, proof values, full SMS destinations, and stored
secret material
**And** no destination is returned by the overview unless a separately documented masked representation is required.

**Given** enrollment expires, is cancelled, verifies successfully, is invalidated, or replaces an active factor
**When** the client performs an authoritative read after the transition
**Then** the response reflects the committed active and pending state
**And** the prior enrollment response is never required as durable client state.

**Given** password, MFA, ban-state, account-state, verified-mobile, or security-generation changes invalidate MFA artifacts
**When** MFA state is read or a mutation is attempted
**Then** stale challenges, enrollments, proofs, and actions are unavailable according to the documented lifecycle
**And** errors do not reveal secret material or hidden account state.

**Given** a supervisor shares an assigned course with a student-only target
**When** the supervisor submits the documented CSRF-protected MFA-reset operation
**Then** MIA atomically removes active and pending factors, recovery codes, challenges, and proofs
**And** the same transaction writes a content-free audit event, invalidates all student cookies, and requires password
replacement after a fresh login.

**Given** a different administrator targets an account with any staff role
**When** the administrator submits the documented MFA-reset operation
**Then** MIA atomically removes the factor and all related security artifacts
**And** the same transaction writes a content-free audit event and restricts existing staff cookies to password
replacement according to the confirmed staff recovery contract.

**Given** a supervisor targets an unrelated or staff account, an administrator targets themself, or HTTP would expose
sole-administrator recovery
**When** reset authorization is evaluated
**Then** MIA applies the existence-safe denial contract without mutation
**And** sole-administrator self-recovery remains available only through the offline operator command.

**Given** an MFA-reset response is definitive, stale, unavailable, or transport-ambiguous
**When** the client handles the result
**Then** definitive success describes only the committed security effects and other outcomes expose a documented safe
reconciliation read
**And** no ambiguous unsafe request is automatically replayed.

**Given** MFA state and reset tests
**When** lifecycle, role-union, assignment, self-targeting, account-state, concurrency, cookie, and redaction cases run
**Then** they prove exact authorization, atomic cleanup, password gates, audit redaction, and state reconciliation
**And** every new read and mutation is documented in OpenAPI with CSRF, errors, rate limits, and `no-store`.

### Story 2.6: Observe and Continue Mobile Verification

As a staff account holder changing my verified mobile number,
I want authoritative verification state and resend timing after reload,
So that I can continue safely without guessing expiry, probing limits, or creating duplicate provider calls.

**Requirements:** FR-35; NFR-1, NFR-2, NFR-6, NFR-10, NFR-11; AR-03, AR-04, AR-10, AR-20;
UX-DR-071 through UX-DR-075

**Acceptance Criteria:**

**Given** the authenticated account has a relevant mobile-change challenge
**When** its verification state is read
**Then** MIA explicitly represents active, expired, invalidated, completed, absent, or unavailable state
**And** an active state includes the server-authored `expires_at`, resend eligibility, and any cooldown `next_resend_at`.

**Given** a mobile-change challenge is created
**When** MIA returns the admitted result
**Then** the response contains the same authoritative lifecycle representation available through the subsequent read
**And** expiry is exactly 30 minutes after issuance without using browser receipt time.

**Given** an active challenge is still within its 60-second resend cooldown
**When** its state is read or resend is requested
**Then** MIA reports resend as unavailable with the authoritative next-resend instant
**And** no SMS provider request is made.

**Given** hourly or daily resend capacity is exhausted
**When** state is read or resend is requested
**Then** MIA distinguishes rate limiting from the ordinary cooldown through a stable documented state or error and
`Retry-After` value
**And** it does not expose thresholds, destination-based state, limiter keys, or attempt counts.

**Given** resend is eligible
**When** MIA attempts it
**Then** it sends the existing code, preserves the original expiry and accumulated verification-failure count, and counts
the provider attempt against durable limits
**And** the response returns the unchanged expiry and equivalent current lifecycle state.

**Given** verification succeeds, five incorrect submissions occur, the verified mobile is removed or replaced, or a
related security change invalidates the challenge
**When** the current state is read
**Then** MIA returns the corresponding completed, invalidated, expired, or absent outcome
**And** a stale code cannot verify or restore the challenge.

**Given** concurrent tabs create, read, resend, verify, replace, or invalidate one challenge
**When** their operations race
**Then** all definitive responses and subsequent reads converge on one committed lifecycle
**And** no race extends expiry, resets failures, reuses a completed code, or produces duplicate provider calls.

**Given** any mobile-verification response
**When** MIA serializes it
**Then** it excludes the destination number, verification code, failure count, limiter identity, and unrelated account data
**And** authorization, CSRF, stable errors, non-refreshing reads, and `no-store` conform to OpenAPI.

**Given** clock-controlled and security tests
**When** expiry, cooldown boundaries, durable limits, provider failures, incorrect submissions, reload, and concurrent
invalidation are exercised
**Then** they prove the documented lifecycle and preserved resend semantics
**And** unauthorized or cross-account reads remain existence-safe.

### Story 2.7: Understand Course Readiness and Confirm Destructive Course Actions

As a student, supervisor, or administrator,
I want authoritative course readiness and reviewed destructive actions,
So that I can take only valid course actions without reconstructing prerequisites or accepting changed consequences.

**Requirements:** FR-37 through FR-43, FR-54; NFR-1, NFR-2, NFR-6; AR-03, AR-04, AR-12, AR-20;
UX-DR-054 through UX-DR-057, UX-DR-067 through UX-DR-070

**Acceptance Criteria:**

**Given** an authorized viewer reads a course
**When** MIA evaluates its current dependencies atomically
**Then** the response distinguishes inactive-incomplete, inactive-activatable, active-accepting, and active-not-accepting
states
**And** activation eligibility and new-session eligibility are returned independently from course state.

**Given** course access was lost or readiness cannot be evaluated safely
**When** readiness is requested
**Then** MIA returns the documented existence-safe access-lost or unavailable outcome without prerequisite details
**And** it does not preserve an earlier eligibility result as authority.

**Given** an assigned supervisor can resolve an unmet activation prerequisite
**When** readiness is returned
**Then** it includes stable blocker identifiers for learning goals, AI tutor instructions, language, supervisor presence,
and qualifying material
**And** each visible blocker may include only an authorized link to the corresponding preparation work.

**Given** a student or another viewer may not inspect preparation details
**When** readiness prevents an action
**Then** MIA returns only viewer-appropriate eligibility and blocker information
**And** it does not disclose material, assignment, or configuration details outside that viewer's scope.

**Given** course fields, supervisor assignments, material processing or approval, activation, or deactivation change
**When** the transaction commits or a later readiness read occurs
**Then** readiness is recomputed from the committed source data rather than a duplicated counter
**And** relevant mutation responses return the equivalent current representation or document its reconciliation read.

**Given** an active course loses its last approved, ready, file-backed course-wide material
**When** readiness is evaluated
**Then** the course remains active but rejects new tutoring sessions
**And** existing tutoring sessions remain resumable and may finish.

**Given** a qualifying material becomes available again
**When** readiness is reevaluated
**Then** the active course accepts new sessions without requiring reactivation
**And** an inactive course becomes activatable only when every prerequisite is satisfied.

**Given** course, supervisor-assignment, or student-membership detail is shown before a destructive action
**When** MIA returns the reviewed representation
**Then** it includes a strong `ETag` covering the state, blockers, and cascade conditions that can change the action's
effect
**And** it returns server-authored eligibility and viewer-safe consequences for that exact action.

**Given** an administrator removes a course supervisor, an assigned supervisor removes a student membership, or an
administrator deletes a course
**When** the request carries the reviewed `If-Match` value
**Then** MIA atomically rechecks authorization, the last-supervisor guard, active-session blockers, course state, and the
precondition
**And** it applies exactly the documented assignment, course-data cascade, account preservation, triage, and audit effects.

**Given** the precondition is missing or stale, access was lost, or a blocker changed after review
**When** a destructive course mutation is submitted
**Then** no removal or deletion occurs and MIA returns the documented precondition or existence-safe unavailable result
**And** the operation is never replayed automatically.

**Given** readiness and mutation tests
**When** every prerequisite transition and concurrent supervisor, membership, material, activation, session, and deletion
race is exercised
**Then** they prove authoritative readiness, exact viewer disclosure, stale no-op behavior, and preservation of existing
sessions
**And** OpenAPI documents all states, blocker identifiers, links, eligibility, `ETag`, `If-Match`, errors, and `no-store`
responses.

### Story 2.8: Discover and Resume the Active Tutoring Session

As a student,
I want to discover my active tutoring session across every course,
So that I can resume it after sign-in, reload, interruption, or device change without creating a duplicate.

**Requirements:** FR-54 through FR-56; NFR-1, NFR-2; AR-03, AR-04, AR-20; UX-DR-011 through UX-DR-015

**Acceptance Criteria:**

**Given** the authenticated student owns one active tutoring session
**When** cross-course active-session discovery is requested
**Then** MIA atomically returns that session regardless of which course currently appears in the browser
**And** the representation includes the session ID plus authorized course ID and name needed for navigation and retrieval.

**Given** the authenticated student owns no active tutoring session
**When** discovery is requested
**Then** MIA returns `200` with a valid JSON:API document whose `data` member is `null`
**And** normal absence remains distinct from sign-in expiry, authorization loss, and dependency failure.

**Given** the course was deactivated after the session started
**When** the owning student performs discovery
**Then** the still-active session is returned and remains resumable
**And** course deactivation does not cause the client to offer or create a replacement session.

**Given** session creation returns `409 tutoring_active_session`
**When** the client reconciles the conflict through discovery
**Then** MIA returns the single existing active session
**And** the client does not enumerate courses or replay creation.

**Given** a session-creation response is lost or transport-ambiguous
**When** the client performs discovery before any further creation attempt
**Then** it receives the committed active session or authoritative `data: null`
**And** a new unsafe request is not automatically replayed.

**Given** two devices concurrently create, finish, discover, or otherwise affect the student's sessions
**When** transactions race
**Then** the one-active-session invariant and discovery operation expose zero or one committed active session
**And** no stale read, cached session ID, or per-course pagination becomes authoritative.

**Given** an unknown, deleted, retained but completed, or out-of-scope session state
**When** discovery or subsequent ID-addressed retrieval occurs
**Then** MIA applies the documented existence-safe result for that operation
**And** it does not reveal another student's session or course context.

**Given** discovery contract and integration tests
**When** login on a new device, reload, browser closure, network loss, ambiguous creation, active-session conflict,
deactivation, and completion are exercised
**Then** every flow resumes the same retained active session or returns authoritative `data: null`
**And** OpenAPI documents the response shapes, non-refreshing behavior, errors, rate limits, and `no-store`.

### Story 2.9: Observe and Control Current Tutoring Work

As a student in an active tutoring session,
I want authoritative current work and action eligibility,
So that I can send, queue, stop, retry, reconnect, and finish without duplicating work or acting on stale tab state.

**Requirements:** FR-55, FR-56, FR-60 through FR-64; NFR-1, NFR-2, NFR-16 through NFR-18; AR-03 through AR-06,
AR-20, AR-22; UX-DR-058 through UX-DR-062

**Acceptance Criteria:**

**Given** the owning student reads an active tutoring session
**When** MIA returns its current work
**Then** one atomic representation identifies idle state, any generating response, any queued message and response, and
remaining queue capacity
**And** every included message and response uses its immutable ID and documented lifecycle state.

**Given** any valid current-work state
**When** MIA constructs the representation
**Then** it returns independent server-authored eligibility for submit, queue, stop, retry, and finish
**And** the client need not infer eligibility from transcript pages, elapsed time, locally observed events, or failed
mutations.

**Given** the session is idle, generating, queued, or generating with one queued item
**When** the student submits or queues a bounded message with its request ID
**Then** MIA enforces at most one generating response and one queued message atomically
**And** the definitive response returns equivalent current work or identifies the immediate authoritative reconciliation
read.

**Given** submit, queue, retry, Stop, or finish returns a conflict, disconnects, or has a transport-ambiguous outcome
**When** the client performs the documented current-work reconciliation read
**Then** MIA returns the committed message, response, queue, retry, Stop target, and session state relevant to that mutation
**And** the client can distinguish committed, rejected, and still-unknown outcomes without replaying the unsafe request.

**Given** current generation becomes completed, failed, or interrupted while work is queued
**When** automatic handoff occurs
**Then** MIA atomically starts only the queued response and preserves both the terminal and newly generating response IDs
**And** current work cannot attribute the queued response's state to the prior response.

**Given** the student requests Stop for an immutable response ID
**When** that response becomes terminal and queued work starts before, during, or after the request
**Then** reconciliation returns or binds both the targeted response's authoritative terminal outcome and the session's
current work
**And** the interface never infers Stop success from the next response's state.

**Given** a failed or interrupted response belongs to an active idle session
**When** the student explicitly retries it with a request ID
**Then** MIA applies existing payload-bound idempotency and creates only the documented linked retry work
**And** retry remains unavailable while generation or queued work exists or after session completion.

**Given** the student attempts to finish the session
**When** MIA atomically checks current work
**Then** completion succeeds only when no response is generating and no message or response is queued
**And** conflict returns equivalent current work without completing or discarding anything.

**Given** the browser disconnects, reloads, reconnects, or another tab changes work
**When** current work is read again or SSE resumes
**Then** MIA returns the persisted authoritative state without starting duplicate provider activity
**And** neither polling, reads, SSE, nor reconnect refreshes the authentication idle deadline.

**Given** the session completed, was deleted, became inaccessible, or a dependency is unavailable
**When** current work or an action is requested
**Then** MIA returns the documented completed or existence-safe unavailable outcome
**And** completed sessions accept no new messages or retries.

**Given** tutoring concurrency and recovery tests
**When** two tabs exercise send, queue, stop, retry, handoff, finish, disconnect, startup recovery, deletion, and scope loss
**Then** they prove identity preservation, capacity invariants, eligibility accuracy, idempotency, and zero duplicate provider
requests
**And** OpenAPI documents all work states, action eligibility, reconciliation shapes, errors, SSE behavior, and `no-store`.

### Story 2.10: Follow Tutoring Summary Generation and Regeneration

As a student or assigned supervisor,
I want authoritative tutoring-summary lifecycle and regeneration eligibility,
So that I can distinguish processing, retry, success, correction, and terminal failure without inferring state from a null
summary.

**Requirements:** FR-63, FR-64, FR-84; NFR-1, NFR-2, NFR-12, NFR-17, NFR-18; AR-03 through AR-07, AR-12, AR-20;
UX-DR-021 through UX-DR-025

**Acceptance Criteria:**

**Given** an authorized viewer reads a completed tutoring session
**When** MIA returns summary state
**Then** it explicitly represents queued, generating, automatic-retry-scheduled, generated, supervisor-corrected,
terminal-failure, or unavailable lifecycle
**And** lifecycle is independent from nullable summary content and generic job-list visibility.

**Given** session completion succeeds
**When** MIA commits the completion transaction
**Then** it atomically queues summary generation and returns or exposes the queued summary lifecycle
**And** transcript availability remains independent from summary processing.

**Given** summary processing fails transiently within the bounded retry policy
**When** a retry is scheduled
**Then** authorized viewers see automatic-retry-scheduled state and the authoritative retry instant
**And** MIA does not expose provider payloads, internal attempt details, or a manual generic-job retry action.

**Given** summary processing exhausts retries or fails permanently
**When** the terminal result commits
**Then** viewers see terminal-failure without successful-looking summary content or an implied automatic retry
**And** the complete retained transcript remains available to its authorized viewers.

**Given** a generated summary is available or an assigned supervisor has corrected it
**When** the session is read
**Then** MIA preserves the documented summary source, correction attribution, and authorized state timestamps
**And** delayed generation or retry results cannot silently overwrite a supervisor correction.

**Given** MIA returns summary lifecycle
**When** timestamps are serialized
**Then** it includes only authoritative state-change time, last-checked time, and authorized scheduled-retry time where
applicable
**And** the client does not derive lifecycle from elapsed time or missing fields.

**Given** a completed session has no summary or current summary job
**When** regeneration eligibility is read
**Then** MIA reports regeneration as allowed only when an earlier summary job failed terminally
**And** every other state reports it as unavailable without requiring a probe mutation.

**Given** an authorized viewer explicitly requests eligible regeneration
**When** MIA rechecks the conditions atomically
**Then** it queues exactly one summary job and returns equivalent current lifecycle
**And** stale, duplicate, ineligible, disconnected, or ambiguous outcomes use the documented reconciliation read without
automatic replay.

**Given** a student, assigned supervisor, unrelated user, deleted session, or lost assignment requests summary state
**When** authorization is evaluated
**Then** MIA applies the exact transcript and summary visibility rules with existence-safe errors
**And** no generic job metadata broadens access.

**Given** summary lifecycle tests
**When** completion, queueing, generation, delayed retry, success, exhausted attempts, correction, regeneration,
concurrency, and deletion are exercised
**Then** they prove lifecycle transitions, eligibility, correction preservation, one-job behavior, and transcript
independence
**And** OpenAPI documents every state, field, action, error, reconciliation response, and `no-store` policy.

### Story 2.11: Request Mentoring and Complete Eligible Work

As a student, mentor, or supervisor,
I want authoritative mentoring eligibility and reviewed lifecycle actions,
So that new requests, scheduled completion, and mentor removal follow current assignments and state without client-side
policy guesses.

**Requirements:** FR-65 through FR-71; NFR-1, NFR-2, NFR-6; AR-03, AR-04, AR-07, AR-12, AR-20;
UX-DR-063 through UX-DR-070, UX-DR-076 through UX-DR-078

**Acceptance Criteria:**

**Given** a student reads new mentoring-request eligibility for a joined course
**When** MIA atomically evaluates course activity, `mentoring_requests_allowed`, and qualifying mentor assignment
**Then** it returns allowed, neutrally disabled, access-lost, or unavailable state
**And** it discloses only reasons the student is authorized to know without exposing settings or mentor identities.

**Given** a mentoring-eligibility request is still in flight
**When** the browser presents checking state
**Then** no prior allowed or disabled result remains authoritative
**And** checking resolves only from the documented allowed, disabled, access-lost, or unavailable response.

**Given** new mentoring requests become disabled by course, setting, assignment, or membership changes
**When** the student rechecks eligibility
**Then** the response reflects current committed state
**And** existing authorized mentoring records remain readable and actionable.

**Given** eligibility was previously allowed but a dependency changes before creation
**When** the student submits a new mentoring request
**Then** MIA rechecks every creation condition atomically and either creates one valid request or returns the documented
race outcome
**And** the client does not probe, infer, cache eligibility as authority, or replay an ambiguous request automatically.

**Given** an authorized viewer reads a mentoring session
**When** MIA evaluates completion for that viewer
**Then** it returns allowed, not-allowed, access-lost, terminal, or unavailable completion state
**And** only the currently assigned mentor can receive an allowed result.

**Given** the assigned mentor is not yet allowed to complete because scheduled time has not begun
**When** completion eligibility is returned
**Then** MIA includes the authoritative UTC instant at which an explicit recheck may be offered
**And** browser clocks, countdowns, sleep, or timer wake never authorize completion.

**Given** assignment, schedule, course, account, scope, or mentoring lifecycle changes
**When** completion eligibility is explicitly rechecked
**Then** MIA evaluates the current state using server time
**And** course deactivation does not prevent authorized handling or completion of existing work.

**Given** the interface is about to enable final completion confirmation
**When** the assigned mentor explicitly requests a fresh eligibility check
**Then** MIA recomputes assignment, schedule, course, account, scope, and lifecycle state immediately before confirmation
**And** only a fresh allowed result permits the confirmation to be shown.

**Given** the assigned mentor confirms completion after receiving an allowed result
**When** MIA processes the completion mutation
**Then** it atomically rechecks assignment, schedule, lifecycle, authorization, and server time before committing
**And** definitive success returns the equivalent current mentoring-session representation.

**Given** completion is stale, ineligible, concurrent, or transport-ambiguous
**When** the result is reconciled
**Then** the current mentoring-session representation establishes requested, scheduled, cancelled, completed, access-lost,
or unavailable state
**And** the client never claims success or automatically replays completion without definitive backend state.

**Given** an authorized supervisor reviews removing a mentor assignment
**When** MIA returns the assignment and affected-work consequences
**Then** it includes a strong `ETag` covering assignment and open-work state that can change the removal effect
**And** the response distinguishes removal consequences from direct reassignment, which preserves schedule and prior
response data.

**Given** the supervisor submits mentor removal with the reviewed `If-Match` value
**When** MIA atomically rechecks state and authorization
**Then** removal returns affected open work to supervisor triage, clears future schedule and meeting details, and preserves
the documented historical data
**And** a missing or stale precondition causes no mutation and requires fresh review and confirmation.

**Given** mentoring authorization, clock, and concurrency tests
**When** course state, settings, first or last mentor assignment, schedule boundaries, browser sleep, reassignment, removal,
completion, and access loss are exercised
**Then** they prove exact request and completion eligibility, existing-work preservation, stale no-op behavior, and
existence-safe disclosure
**And** OpenAPI documents all states, recheck instants, preconditions, reconciliation shapes, errors, and `no-store`.

### Story 2.12: Resolve and Grant Mentor Targets Safely

As a supervisor,
I want to resolve one exact registered-staff target before granting the mentor role,
So that I can prevent a wrong-person grant without gaining access to the administrator account directory.

**Requirements:** FR-13; NFR-1, NFR-2, NFR-6, NFR-9, NFR-11, NFR-13; AR-03, AR-04, AR-07, AR-12, AR-20;
UX-DR-067 through UX-DR-070, UX-DR-079 through UX-DR-083

**Acceptance Criteria:**

**Given** an authenticated supervisor knows one complete opaque user ID
**When** they submit it to the focused CSRF-protected non-mutating POST preflight
**Then** MIA validates one bounded exact ID and performs no substring, prefix, fuzzy, batch, browse, autocomplete,
suggestion, pagination, or result-count operation
**And** the preflight creates no durable target-resolution record.

**Given** the exact target is an eligible registered staff account with verified email
**When** preflight succeeds
**Then** MIA returns only immutable user ID, username, nullable display name, and server-authored mentor-role grant state
**And** it excludes email, mobile, other profile data, unrelated roles, assignments, courses, security state, ban details,
and administrator metadata.

**Given** the target already has the permanent mentor role
**When** preflight succeeds
**Then** the action state authoritatively reports the existing grant
**And** the later grant operation remains idempotent without implying a new role change.

**Given** the target is unknown, student-only, unverified, banned, deleted, inaccessible, or otherwise ineligible, or the
resolution is throttled
**When** preflight is evaluated
**Then** every cause returns the same fixed unavailable status, body, headers, and non-identifier-specific timing behavior
**And** no target-specific `Retry-After`, rejected identity, eligibility reason, or partial match is exposed or retained.

**Given** an eligible target is returned for confirmation
**When** MIA constructs the review representation
**Then** it includes a strong `ETag` covering identity, account class and state, verified-email eligibility, and current
mentor-role state
**And** the frontend can confirm only the exact target and exact mentor-role action represented by that preflight.

**Given** the supervisor confirms the mentor-role grant with the reviewed `If-Match` value
**When** MIA processes the existing grant mutation
**Then** it atomically rechecks supervisor authority, exact target, staff and email eligibility, account and ban state,
current role state, and the precondition
**And** it grants only the permanent mentor role or returns idempotent already-granted success.

**Given** target eligibility or identity-relevant state changes after preflight, or `If-Match` is absent
**When** grant is submitted
**Then** MIA returns the documented precondition-required or stale-precondition outcome without granting a role
**And** a fresh preflight and explicit new confirmation are required.

**Given** the grant response is denied, unavailable, sign-in-expired, or transport-ambiguous
**When** the client reconciles the outcome
**Then** a new preflight may reveal only unavailable, grant-available, or already-granted state
**And** the unsafe grant is never replayed automatically.

**Given** supervisor target resolution is unavailable
**When** the supervisor uses other mentoring or account features
**Then** existing mentoring reads, responses, scheduling, assignments, reassignment, removal, triage, and authorized
administrator account reads remain usable
**And** blocking remains scoped only to direct supervisor mentor-role grant.

**Given** authorization, enumeration-resistance, and concurrency tests
**When** actor roles, sequential and malformed IDs, repeated misses, throttling, target role, email, ban, deletion, and
account-state races are exercised
**Then** they prove exact matching, response indistinguishability, bounded limiter state, minimal disclosure, stale no-op,
and idempotent success
**And** OpenAPI documents preflight, grant preconditions, reconciliation, CSRF, limits, errors, and `no-store`.
