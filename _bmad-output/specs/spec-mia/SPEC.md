---
id: SPEC-mia
companions:
  - ../planning-artifacts/prds/prd-mia-2026-08-29/prd.md
  - ../planning-artifacts/prds/prd-mia-2026-08-29/addendum.md
  - ../planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md
sources: []
---

> **Canonical contract.** This SPEC and the files in `companions:` are the complete, preservation-validated
> contract for what to build, test, and validate. Source documents listed in frontmatter are for traceability —
> consult them only if you need narrative rationale or prose color this contract intentionally omits.

# MIA — Self-Hosted AI Tutoring Platform, Backend MVP

## Why

Students preparing for current classes get little value from generic tutoring platforms whose content ignores
their school's curriculum, pacing, and teaching approach; teachers hold the right material but have no safe,
supervised way to turn it into personalized AI tutoring. MIA closes that gap: a self-hosted, browser-based
platform where an AI tutor works exclusively from supervisor-approved school material under human oversight —
supervisors approve material briefs, review completed sessions, and tune instructions per course and student,
with human mentors as a deliberate last resource. It processes sensitive educational data of a population that
may include minors, so security, privacy, and responsible-AI constraints are product requirements, not
hardening. This spec covers the backend (single Go binary + SQLite); the frontend ships from a separate
repository.

The adopted PRD (`prd.md`, FR-1..FR-84, NFR-1..NFR-21, AS-1..AS-32) is the binding behavior contract; the
addendum carries confirmed technical depth (deadlines, job mechanics, data layout, API conventions); the
architecture spine (AD-1..AD-14, conventions, stack) binds implementation structure. Capabilities below index
that contract — they never override it.

## Capabilities

- **CAP-1** Identity and account data (FR-1..5)
  - **intent:** The system maintains user accounts with strictly validated identity and profile data
    (username, optional email, language, country, time zone, bounded optional profile text).
  - **success:** Inputs outside the documented bounds are rejected; username/email uniqueness compares by ASCII
    lowercase; passwords and chat messages are never trimmed or normalized.

- **CAP-2** Invitation-only staff registration and role grants (FR-6..14)
  - **intent:** Staff join only by single-use, non-expiring email invitation or direct role grant; the first
    administrator is created by a local offline `bootstrap-admin` command; there is no public sign-up.
  - **success:** AS-20 and AS-21 pass; acceptance verifies the email and consumes the invitation exactly once;
    tokens are persisted only as SHA-256 digests.

- **CAP-3** Student provisioning and course membership (FR-15..17)
  - **intent:** An assigned supervisor provisions student accounts with temporary passwords (no email needed)
    and adds existing students to active courses by exact username.
  - **success:** AS-22 passes; unknown usernames and non-student accounts fail identically; re-adding a member
    is idempotent; rejoin never restores previously deleted course-scoped data.

- **CAP-4** Password policy and recovery (FR-18..21)
  - **intent:** Passwords are 12–128 Unicode code points with no character-class rules and a local
    known-common blocklist; staff recover via 30-minute emailed reset links, students via supervisor-set
    temporary passwords.
  - **success:** AS-23 passes; supervisor reset invalidates all student cookies; public recovery responses
    never reveal account existence (AS-14).

- **CAP-5** Stateless browser sessions and staged login (FR-22..25)
  - **intent:** Authentication uses one signed+encrypted stateless cookie with staged login
    (password → `mfa` → `password-change`) and per-request state reload.
  - **success:** AS-24 passes (30-minute idle, 12-hour absolute); ban/deletion/security-generation changes take
    effect on the next request; restricted stages allow only their enumerated actions.

- **CAP-6** Optional MFA (FR-26..32)
  - **intent:** Any user may enroll one TOTP or SMS factor with single-use recovery codes; supervisors reset
    student MFA, a different administrator resets staff MFA, and a local `reset-admin-mfa` command covers the
    sole-administrator case.
  - **success:** Challenges expire and lock out per the documented limits; a TOTP step succeeds once per
    factor; factor mutations require a single-use `mfa-management` proof consumed atomically.

- **CAP-7** Profiles, avatars, and mobile verification (FR-33..36)
  - **intent:** Staff edit their own non-security profile fields; supervisors edit student profiles
    (students cannot); avatars and logos are normalized to bounded metadata-free PNGs; staff mobile changes
    confirm via SMS.
  - **success:** Student self-edit is rejected; the untrusted image source is never served; avatar/logo
    download inherits profile/course view authorization.

- **CAP-8** Course lifecycle and membership (FR-37..44)
  - **intent:** Administrators create courses with supervisors; supervisors prepare, activate, deactivate, and
    populate them; activation gates on goals, instructions, language, and approved ready file-backed material.
  - **success:** AS-1, AS-2, AS-17, AS-26, and AS-27 pass; every course always keeps at least one supervisor;
    student removal deletes course-scoped data while preserving the account.

- **CAP-9** Learning material and processing (FR-45..53)
  - **intent:** Supervisors manage approval-gated course-wide material and students own private material;
    uploads are bounded and validated, OCR/local extraction produces per-file `content.jsonl`, and each
    material gets a grounded supervisor-correctable brief.
  - **success:** AS-3, AS-4, AS-5, and AS-10 pass; any failed file makes the whole material faulty; brief
    edits on approved material atomically revoke approval; regeneration never silently overwrites supervisor
    edits.

- **CAP-10** Tutoring sessions with a grounded, budgeted AI tutor (FR-54..64)
  - **intent:** A student holds at most one active chat session in a joined active course; the tutor works from
    fixed context plus authorized bounded retrieval, streams responses, and produces a supervisor-correctable
    summary on completion.
  - **success:** AS-6, AS-7, AS-8, AS-18, AS-25, and AS-28..32 pass; MIA authorizes every retrieval; sessions
    survive disconnects and never expire from inactivity; only the owning student finishes a session.

- **CAP-11** Mentoring as last resource (FR-65..71)
  - **intent:** After the tutor exhausts suitable approaches, students with an assigned mentor may request
    human mentoring; supervisors triage and assign, mentors respond and schedule sessions held outside MIA.
  - **success:** AS-11, AS-12, and AS-13 pass; mentors receive only the topic — never uploads or chat history;
    mentor removal returns open work to triage in one transaction.

- **CAP-12** Text-to-speech (FR-72..73)
  - **intent:** Students can request MP3 speech for completed tutor responses via ElevenLabs, with bounded
    caching and configurable retention.
  - **success:** Without configured ElevenLabs the feature returns a stable unavailable error; cached speech is
    reused only while source content and voice match; expired speech is deleted and regenerable.

- **CAP-13** Student bans (FR-74)
  - **intent:** A supervisor sharing an assigned course can ban and unban student-only accounts.
  - **success:** A ban rejects new logins and the next request on existing cookies; banned accounts cannot use
    or complete recovery, indistinguishably; staff accounts cannot be banned.

- **CAP-14** Account deletion and data lifecycle (FR-75..78)
  - **intent:** Administrators delete accounts per the documented safety rules; deletion removes operational
    data and keeps only content-free audit records with de-identified fingerprints.
  - **success:** AS-16 passes; deleting the last administrator or a sole course supervisor is rejected; late
    asynchronous results against deleted targets are discarded, never upserted or retried.

- **CAP-15** Audit log (FR-79..80)
  - **intent:** Administrators inspect a content-free audit log of security-relevant events written atomically
    with their mutations.
  - **success:** Audited event classes match FR-79; audit content never contains the values excluded by FR-80;
    ordinary denied reads are not audited.

- **CAP-16** Background jobs and oversight (FR-83..84)
  - **intent:** Durable jobs (extraction, summaries) run through a leased single-worker queue with bounded
    automatic retries; administrators inspect jobs, supervisors see safe processing status only.
  - **success:** Jobs retry ≤3 times for transient failures only; no generic job-retry operation exists;
    guarded commits prevent duplicate durable output (AS-10).

## Constraints

- The FR/NFR text in the adopted PRD is the binding, launch-blocking contract; every capability above indexes
  it. On conflict, the PRD governs product behavior and the spine governs implementation structure.
- Architecture invariants AD-1..AD-14 plus the spine's consistency conventions, stack table, and structural
  seed bind all implementation (vertical feature packages over a thin kernel; auth is the first slice).
- Security baseline NFR-1..8 applies everywhere: explicit authorization on every sensitive resource,
  per-endpoint existence-hiding, untrusted-input handling, Argon2id passwords, digest-only bearer tokens,
  strict redaction — the population may include minors.
- Model output never authorizes a platform action; tutor requests use fixed 32,000-token input / 2,048-token
  output budgets with bounded retrieval (≤3 rounds, ≤8 excerpts, ≤4,000 code points each).
- One dependency-free Linux binary (Go ≥1.27, Echo 5.3.1, CGo-free SQLite, embedded forward-only migrations);
  SQLite plus the data directory form one consistent backup set; single process, single node, no HA claim.
- External provider availability is never a startup prerequisite; outages degrade dependent features visibly
  and never invent success; request-path calls never auto-retry after ambiguous failure.
- Rate limits and password/session/MFA policies are fixed by MIA, not operator-configurable; operator tuning is
  limited to the documented upload caps, speech retention, and model selection.
- Every API instant uses RFC 3339 UTC with the `Z` suffix; users have preferred IANA time zones for display
  (FR-81).
- Automated tests never call paid or production providers (NFR-21).

## Non-goals

- No predefined courses, curricula, or learning material shipped with the product.
- No public sign-up or self-registration of any kind.
- No guardian accounts, age verification, or guardian-consent workflow (operator responsibility).
- No guarantee of academic results.
- No emergency service, automated safeguarding alerts, or real-time human monitoring.
- No malware scanning and no claim that accepted files are malware-free.
- No copyright or license validation and no uploader attestation.
- No server-side fetching of external material URLs; model output can never trigger network retrieval.
- No data-export feature.
- No workflow notifications, subscriptions, quiet hours, or workflow SMS (FR-82).
- No live mentor chat, audio, or video; no attendance or no-show tracking.
- No monetary budgets or provider spending limits; usage counters are operational, not billing data.
- MVP exclusions (PRD §3): no server-side browser-session records or remote revocation, no cookie revocation
  after staff password reset, immutable staff email, no reopening ready material, no field encryption, no
  separate authenticated tutoring/upload/finalization/speech rate limits, no student self-profile editing.
- The frontend: maintained and released from a separate repository; this spec covers the backend only.

## Success signal

The MVP is launch-ready when all 32 acceptance scenarios (AS-1 through AS-32, PRD §11) pass and no critical
security findings are open. Quantitative outcome metrics are deliberately deferred until real deployments exist,
consistent with MIA's no-telemetry, self-hosted posture (PRD §12).

## Open Questions

Carried from PRD §13; downstream must not guess these. Per-route API contracts are deliberately deferred per
slice (auth first) and are not listed here.

- The audit action-name allowlist grows per implemented slice; the full list is open.

Product-owner decisions of 2026-08-29 resolved the previously listed items (brief per-field limits, baseline
CSP, no exposed brief regeneration on ready material, interrupted-response retry eligibility, active-session
start/last-activity instants, accepted NAT rate-limit risk, indistinguishable non-pending invitation tokens,
final-stage 12-hour anchor, corrected summary feeding the next session, and the mentor minimal-identity field
set). They are folded into the adopted PRD (FR-11, FR-22, FR-49, FR-57, FR-62, FR-63, NFR-8, §4, §13) and
`docs/product-requirements.md`; the PRD text remains the binding contract.
