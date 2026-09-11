# Repository guidance

## Precedence

- Follow user instructions first, then this file, then the scoped rules in
  `.agents/rules/`.
- More specific rules override general rules when both apply.
- Product documentation defines intended behavior. Agent rules govern how work
  is performed; they must not invent product behavior.
- If documents conflict, identify the conflict and ask which behavior is
  authoritative before implementing it.

## Authorization and assumptions

- When the user asks a question or requests a review, do not make code changes
  unless they explicitly request them.
- Bug reports, logs, reproduction steps, and statements that something appears
  broken are requests for diagnosis, not implicit authorization to edit.
- When the user explicitly asks to change, fix, or implement something, carry
  the work through implementation and verification.
- Do not guess at missing product behavior, authorization boundaries, data
  lifecycle rules, or security requirements. Ask concise, grouped questions
  when answers are required to proceed safely.
- If requested constraints cannot all hold, stop and explain the exact conflict.
  Do not silently relax a constraint or implement an approximation.
- If an instruction appears mistaken or conflicts with established behavior,
  state that directly instead of silently working around it.
- Do not add speculative abstractions or compatibility layers. Add indirection
  only for a current, concrete need.
- Reuse an existing type when it represents the same concept and contract. Use
  separate transport, domain, or persistence types when representation,
  ownership, serialization, or invariants genuinely differ.
- Keep one source of truth. Do not duplicate facts in counters, metadata, or
  indexes unless the product requires a separately maintained projection.
- If the user refers to the latest screenshot, use the newest file in
  `./tmp/screenshots/`.

## Current project state

- MIA is currently in product and API planning. The repository contains
  documentation and quality configuration but no Go module or application code.
- This repository is the backend. The frontend is maintained and released from
  a separate repository.
- MIA is a self-hosted Linux server used through a browser. It is not a desktop
  application.
- MIA has no public sign-up endpoint or self-registration workflow. Supervisor
  and mentor registration is invitation-only; student accounts are provisioned
  by assigned supervisors.
- The first administrator is created through a one-time local operator bootstrap
  command while the server is stopped. It reads the password twice from a
  terminal, is available only while no administrator exists, and has no public
  web route.
- Students do not require email addresses. A supervisor provisions a student
  account and chooses its username and temporary initial password. The student
  must replace it at first login before using other authenticated features.
- Any supervisor sharing an assigned course with a student-only account can set a
  temporary password for recovery. Invalidate all existing student cookies and
  require replacement after a fresh login. Never log or audit password values.
- Administrators, supervisors, and mentors require verified email addresses.
  Acceptance of an invitation delivered to that address verifies it.
- New administrators, supervisors, and mentors choose their own username and
  password during invitation acceptance; the inviter does not issue temporary
  credentials.
- A mentor invitation creates only a new account and permanent mentor role; it
  has no course or student scope. Any supervisor may invite a mentor. Only the
  inviting supervisor or an administrator may manage that invitation.
- Invitations register new accounts only. Additional permanent roles are granted
  directly only to registered staff accounts by user ID, take effect immediately,
  and require no user approval. Granting the supervisor role also grants the
  student role in the same transaction.
- Any staff role determines staff profile, password-recovery, MFA-recovery, ban,
  and deletion behavior. Student administration operations target student-only
  accounts. A banned account must be unbanned before receiving a staff role.
- An assigned supervisor adds a registered mentor to a course and assigns a
  course mentor to students. These assignments take effect immediately without
  mentor acceptance, and mentors cannot reject or remove their assignments.
- Administrator, supervisor, and mentor invitations do not expire; they remain
  pending until accepted or revoked unless definite initial SMTP failure makes
  them faulty. SMTP timeout leaves them pending and usable, is logged, and is not
  retried automatically.
- Invitation DELETE revokes a pending invitation, physically deletes a faulty
  invitation, and rejects accepted or revoked invitations.
- Invitation acceptance is single-use and permanently consumes the invitation.
- Invitation and password-reset bearer tokens are canonical lowercase UUID v4
  values. Persist only their SHA-256 digests.
- Login and staff password recovery accept username only. Recovery sends a
  single-use link to the verified email that expires after 30 minutes. New
  requests leave earlier links valid; successful reset invalidates the rest.
  Public recovery responses do not reveal account existence. The MVP does not
  revoke other stateless browser cookies after reset; they expire normally.
- Passwords are 12 to 128 characters, allow spaces and Unicode, and have no
  character-class requirements. Reject known-common passwords locally. Do not
  trim passwords or allow operators to weaken the policy.
- Usernames use 3-32 characters from a restricted ASCII set and compare by ASCII
  lowercase. Emails use a practical ASCII subset, are limited to 254 characters,
  and compare by ASCII lowercase. Password length counts Unicode code points.
- Optional profile text uses null as its sole absent representation. Names are
  limited to 100 Unicode code points, nicknames to 24, and student-specific AI
  instructions to 4,000 code points and 16 KiB. Mobile numbers use strict E.164.
- The MVP applies no application-layer encryption to SQLite fields. TOTP secrets
  and active SMS codes are stored in plaintext and rely on private data-directory
  and backup access. Never log, audit, or expose them.
- Authenticated browser sessions expire after 30 minutes of inactivity and no
  later than 12 hours after authentication. Activity can reset only the idle
  timeout.
- The MVP uses signed and encrypted Echo CookieStore sessions and keeps no
  server-side browser-session records. Account ban and deletion are checked on
  every request. A student's one-active-tutoring-session rule applies across
  devices.
- Only student-only accounts can be banned. A ban rejects the next request but
  does not cancel provider or background work already in flight.
- Login uses one rotated CookieStore value with restricted `mfa` and
  `password-change` stages before the full `authenticated` stage. When both are
  required, MFA precedes password replacement.
- MFA is optional for every user and role. Do not impose role-based MFA
  enrollment.
- Supported MFA methods are TOTP and SMS. SMS enrollment or replacement requires
  ClickSend and a verified profile mobile; active login challenges use the
  factor's immutable destination snapshot after profile-mobile change or removal.
  Enrollment requires verification before activation.
- TOTP uses SHA-1, six digits, 30-second steps, one-step clock skew, and a 20-byte
  secret. SMS codes use six decimal digits. Pending enrollment expires after 30
  minutes. Five failed submissions invalidate an MFA challenge.
- Login MFA challenges last 30 non-refreshing minutes and may coexist across
  login attempts. A TOTP step succeeds only once per factor. Pending-enrollment
  and login-challenge SMS resends reuse the same code, expiry, and failure count.
- MFA disable or replacement uses a five-minute, single-use opaque
  `mfa-management` proof stored only by SHA-256 digest and consumed atomically.
  Password, MFA, ban-state, or account-state changes invalidate all outstanding
  MFA challenges and proofs.
- MFA enrollment issues single-use recovery codes shown once and stored only as
  non-reversible values. There is no standalone regeneration; factor replacement
  invalidates old codes and issues the new set. Never log or audit recovery-code
  values.
- An assigned supervisor may reset lost MFA for a student-only account as a
  separate security action. Invalidate recovery codes and all existing student
  cookies, then require password replacement after a fresh login.
- Staff MFA reset requires a different administrator. When exactly one
  administrator exists, an interactive local command run while the server is
  stopped may reset that administrator's MFA. Never expose this action through a
  public web route.
- Additional administrators join by invitation or direct role grant to registered
  staff. Administrator, supervisor, and mentor roles are permanent. Only a
  different administrator can delete a staff account. Reject deletion of the last
  administrator or a sole course supervisor; otherwise remove current
  assignments, triage open work, and clear historical actor references atomically.
  Only administrators delete student-only accounts. Any staff role makes all
  staff security and deletion rules apply.
- The server is expected to serve a separately installed frontend from a
  configured document root.
- Structured records are stored in SQLite. Uploaded and generated files are
  stored in the data directory. Both form one consistent data set.
- Startup deletes expired speech rows and files first, then marks stranded speech
  generation failed and removes incomplete output. Missing database-referenced
  source, processed JSONL, or unexpired available speech files then make startup
  fail. Missing avatars and course logos are normal.
- OpenAI and Mistral are external processors. SMTP is required for email;
  ClickSend and ElevenLabs support optional features.
- External provider availability is not a startup prerequisite. Startup validates
  provider settings locally and operations use fixed deadlines. Request-path
  provider calls do not retry automatically after ambiguous failure.
- Background retries use current configuration and processing code. MIA does not
  promise identical provider requests or provider-side idempotency across
  attempts; guarded commits still prevent duplicate durable output.
- Echo 5.3.1 is the confirmed web framework. Follow
  `.agents/rules/echo.md` for framework-specific rules.
- Course-wide material has a revocable supervisor approval flag. Only approved
  course-wide material is visible to course students or usable by the AI tutor.
- Changing the validated brief content of approved course-wide material
  atomically revokes approval; unchanged content does not. The revised brief must
  be explicitly approved again.
- An active course with no approved, ready, file-backed course-wide material
  accepts no new tutoring sessions. Existing sessions may finish without access
  to revoked material.
- Courses are either inactive or active. There is no separate archive state.
- Course activation requires learning goals, AI instructions, a valid language,
  at least one supervisor, and approved, ready, file-backed course-wide material.
  Only active courses accept new student memberships.
- Every course keeps at least one supervisor; only administrators remove
  supervisor assignments. An assigned supervisor may remove a student only when
  no active tutoring session exists, and removal deletes all of that student's
  course-scoped data while preserving the account and other-course data.
- Only an administrator can delete a course, and only while it is inactive with
  no active tutoring sessions.
- Material uploaded by a student is accessible to that student, the AI tutor,
  and supervisors assigned to the course. It requires no approval. Mentors,
  other students, unrelated supervisors, and administrators who are not assigned
  as supervisors have no access.
- A student can delete their private material. Delete its source files and
  generated brief and prevent future retrieval. Preserve completed chats,
  session summaries, and a non-content audit record. Quoted historical content
  may remain without a retrievable source.
- Material selected by an active tutoring session cannot be deleted. After the
  session is completed, normal material-deletion rules apply.
- Material selection at session start is optional. The AI tutor receives the
  identity and brief of material selected by the student, if any, not every
  complete source. Once the intent is clear, it may discover and request bounded
  excerpts from ready, file-backed, approved course-wide material and ready,
  file-backed private material owned by the active student in the session's
  course. MIA authorizes every retrieval request and records material used.
- Material content uses strict per-source `content.jsonl`. Search uses normalized
  Unicode terms and deterministic ranking. Material counts as used only when
  content reaches the tutor model, not when search merely finds it.
- Require every API instant in requests and responses to use RFC 3339 UTC with
  the `Z` suffix. Persist instants in UTC. Every user has a preferred IANA time
  zone for display and server-generated communications. Local-time schedules
  use separate local-time and IANA time-zone fields when their meaning must
  survive daylight-saving changes.
- Student-only accounts cannot change profile fields. Any supervisor sharing an
  assigned course with a student-only account can edit that student's
  non-security profile fields. A supervisor-entered student mobile is immediately
  verified and invalidates pending mobile challenges and pending SMS factors.
  Only such an assigned supervisor may set, change, or clear a student-only
  account's ElevenLabs voice; students cannot select it themselves.
  Staff may edit their own non-security profile fields and remove their verified
  profile mobile; removal invalidates pending SMS factors but does not change an
  active SMS factor destination. Staff email is immutable.
- Mentoring is a last resource after the AI tutor has tried suitable educational
  approaches and authorized material. With no assigned mentor, all
  new student-facing mentoring requests are disabled. A supervisor may remove
  any mentor; affected open work returns to supervisor triage with future schedule
  details cleared. `mentoring_requests_allowed` independently controls new
  requests.
- Direct reassignment of open mentoring work preserves schedule, meeting details,
  prior response, and response authorship; it is distinct from mentor removal.
- The Go module path is `github.com/thorstenkramm/mia`, and the minimum supported
  Go version is 1.27.0. Initial packages cover only command wiring,
  configuration, locking, SQLite/migrations, identity, HTTP, and the first auth
  slice; add feature packages only with their implementation.
- MIA ships one `mia` executable with `serve`, `bootstrap-admin`, and
  `reset-admin-mfa` subcommands.
- Every database-using command holds an exclusive OS lock on
  `main.data_dir/mia.lock`. Offline commands validate only their required
  configuration subset and never contact providers.
- MIA uses `modernc.org/sqlite` with the fixed `data_dir/mia.sqlite3` path, WAL,
  `synchronous=FULL`, foreign keys, and a five-second busy timeout. Embedded
  `golang-migrate` v4 up migrations run automatically before database use.
- SQLite uses four open and four idle connections with no connection-lifetime
  expiry.

## Product documentation

- `README.md` is the public product overview.
- `docs/README.md` is the audience-based documentation index.
- `docs/product-requirements.md` is the contract for confirmed product behavior.
- `docs/api.md` is the current API layout draft. It is not authoritative for
  product behavior.
- Other files in `docs/` contain current domain and operational drafts.
- The current documents are incomplete. Do not interpret silence as a product
  decision.
- Programming and architecture decisions must be documented separately from
  product behavior and agent conduct.
- When changing behavior, update every directly affected human-readable contract
  in the same task.

## Product scope and vocabulary

- **AI tutor** means the model-driven tutoring behavior. It is not a user role.
- **Administrator** means a user with named global platform capabilities.
- **Supervisor** means a user assigned responsibility for a course.
- **Mentor** means a user assigned to support a specific student in a course.
- **Student** means a user receiving tutoring in joined courses.
- Use **supervisor** consistently for course responsibility. Do not introduce
  another role for a person's relationship to the course outside MIA.
- Authorization is not determined by role flags alone. Course assignment,
  student assignment, resource ownership, and the requested action are part of
  the decision.
- Multiple roles grant the union of permissions that apply in the relevant
  scope. They do not broaden course or student scope.
- Course names are globally unique. Material names are unique within a course.
  All files in one material use one supported format. Username and non-empty
  email uniqueness are case-insensitive.

## Security and privacy baseline

MIA processes sensitive educational data and may serve minors. Treat security,
privacy, and responsible AI requirements as product constraints, not optional
hardening.

- Apply explicit authorization to every sensitive resource and field.
- Do not reveal whether an out-of-scope student, session, upload, invitation, or
  other sensitive resource exists.
- Never log or expose passwords, password hashes, MFA values or secrets, session
  tokens, API credentials, authorization headers, cookies, private prompts,
  message bodies, provider payloads, or unnecessary personal data.
- Treat uploads, OCR output, external content, user messages, and model output as
  untrusted input.
- Model output must never authorize a platform action.
- Tutor requests use a fixed 32,000-token input budget and 2,048-token output
  limit. Student messages, retrieval rounds, excerpt counts, and excerpt sizes
  are bounded. Material search remains local and index-free in the MVP.
- Keep uploaded and generated files outside the public static root. Authorize
  every download.
- Avatar uploads accept bounded JPEG or PNG, are decoded and normalized to a
  metadata-free PNG no larger than 512 pixels per dimension, and inherit profile
  view authorization.
- Upload limits are operator-configurable within fixed MIA hard caps. Never allow
  unbounded file size, file count, material size, page count, decoded image size,
  or archive expansion.
- MIA has no malware-scanner integration. Treat files as untrusted, validate
  signatures and detected media types, never execute active content, and use
  safe download headers. Do not claim accepted files are malware-free.
- MIA performs no copyright or license validation and requests no uploader
  attestation. The uploader and operator are responsible for content rights.
- Website and YouTube material stores link metadata only. Never fetch external
  material URLs server-side or let model output trigger network retrieval.
- External material links use HTTPS only; YouTube links are restricted to
  recognized YouTube hosts.
- Use bounded input sizes, explicit timeouts, cancellation, and bounded retries
  for external operations.
- Rate limit every unauthenticated API endpoint. Authentication-sensitive routes
  use layered IP and account or challenge limits without revealing resource
  existence. The MVP has no separate authenticated tutoring, upload,
  finalization, or speech rate limits. Bound process limiter state to 50,000
  expiring LRU keys.
- Trust `X-Forwarded-For` only from configured proxies, loopback peers, or the
  permission-controlled Unix listener. Parse bounded chains right-to-left and use
  a safe peer or local fallback for malformed input.
- Never call paid or production providers from automated tests.
- Generated speech retention is configured in days and defaults to 30 days from
  generation. Access does not extend retention. Reuse cached speech only while
  its source message and requested voice match.
- Startup marks stranded speech generation failed and removes incomplete output;
  it never repeats the uncertain provider request automatically.
- Do not claim that self-hosting keeps all processing local. Document data sent
  to every external provider.
- The operator who runs a MIA server is responsible for user eligibility,
  required consent, and applicable local and institutional policies. MIA has no
  guardian accounts, age verification, or guardian-consent workflow.
- Retain operational data until authorized deletion. Student deletion removes
  all of that student's operational data; course deletion removes course-scoped
  data but preserves user accounts. Keep only a minimal, content-free audit
  record. MIA deletes local live data only; the operator controls provider data
  and backups. MIA has no data-export feature.
- Destructive operations do not coordinate with running workers or providers.
  Late commits update existing targets only; zero-row updates discard results,
  remove newly published output, and never upsert, requeue, or retry.
- MIA is not an emergency service and does not send automated safeguarding
  alerts. The AI tutor responds supportively and must not imply that anyone was
  notified or is monitoring the conversation. Supervisors provide local
  guidance outside MIA. Mentoring is not an emergency channel.
- Assigned supervisors may see active-session status but cannot read chat
  messages until the session is completed.
- Tutoring sessions do not expire from inactivity. An authenticated student can
  resume the same active session from any device.
- Completed tutoring sessions accept no new messages or response retries.
  Request-ID history exists only while its owning data remains retained.
- A tutoring session has at most one generating response and one queued message.
  Startup resumes queued work but fails stranded generation rather than risking a
  duplicate provider request.
- Students cannot abandon tutoring sessions; finishing is their only action for
  ending one.
- Only the owning student can finish an active tutoring session. Supervisors and
  administrators cannot force completion.

## General coding priorities

- Prefer simple, readable code and the smallest coherent local change.
- Keep changes scoped, but make touched code internally consistent. Do not leave
  half-converted patterns in the same responsibility.
- Follow established local style when it remains compatible with current rules.
- Do not rewrite useful user-authored comments unless the change makes them
  inaccurate.
- Prefer clear control flow over blanket rules about early returns, nesting,
  `break`, or `continue`.
- Extract a helper when it creates a meaningful abstraction, is reused, or
  substantially improves readability. Do not add forwarding helpers that merely
  rename one call.
- Make I/O and other side effects visible at appropriate boundaries.
- Do not force zero duplication. Remove duplication when one abstraction has a
  clear responsibility and improves maintenance.

## Errors and invariants

- Distinguish normal conditions, invalid external input, expected concurrent or
  stale state, dependency failures, persisted-data problems, and true internal
  invariant violations.
- Return or translate errors for invalid input, authorization failures,
  dependency failures, storage failures, persisted-data inconsistencies, and
  other conditions a server must handle without terminating.
- Panic only for a genuine internal invariant violation where continuing would
  indicate a programming error. Do not use panic for unresolved requirements,
  request-derived state, provider responses, or recoverable job failures.
- Do not add silent fallback or no-op behavior for unspecified states.
- Do not swallow errors with blank identifiers, empty results, or debug-only
  logging.
- Preserve error identity when callers need programmatic handling and add useful
  context when wrapping errors.
- Never commit `panic("TODO")` or equivalent placeholders for unresolved
  behavior. Ask for the missing decision or leave that work unimplemented.

## Go guidance

- Follow `.agents/rules/golang.md` and `.agents/rules/techstack.md` where they do
  not conflict with this file or current product decisions.
- Use `gofmt` and idiomatic package and identifier names.
- Keep `main` focused on configuration and dependency wiring.
- Pass `context.Context` through request, database, job, and provider flows.
- Avoid mutable global state beyond deliberate process-level wiring.
- Close HTTP response bodies, database rows, files, transactions, and other
  owned resources on every path.
- Use parameterized SQL. Do not build queries by concatenating untrusted values.
- Keep package responsibilities clear; do not create `util` or `misc` dumping
  grounds.
- Use a single-field struct when it establishes useful semantics, ownership,
  synchronization, lifecycle, or a method-bearing abstraction. Do not add one
  merely to rename a value.
- Document concurrency guarantees for exported types when concurrent use is
  relevant.
- Follow `.agents/rules/echo.md` for handlers, middleware, server operation, and
  HTTP tests.

## Testing

- Test observable behavior, domain invariants, regressions, and security
  boundaries. Do not add tests that merely mirror constants or freeze incidental
  implementation details.
- Choose table-driven tests, subtests, assertion libraries, and parallelism when
  they improve the test. They are not mandatory patterns.
- Do not call `t.Parallel()` when tests share SQLite databases, environment
  variables, ports, fixture directories, global configuration, or other mutable
  state unless isolation is proven.
- Use real migrations in SQLite integration tests.
- Test authorization across role, course, student, action, and ownership scope.
- Test provider timeouts, cancellation, malformed responses, throttling, and
  outages through local fakes or `httptest` servers.
- Add upload, traversal, SSRF, and input-limit regression tests where relevant.
- Test background-job retry, idempotency, duplicate delivery, and crash recovery
  when those behaviors are introduced.

## Workflow and verification

- Work from the repository root unless a command requires a narrower working
  directory.
- Inspect existing files and nearby conventions before editing.
- Do not revert or overwrite unrelated user changes.
- Do not hide warnings with `nolint`, `lint:ignore`, blank assignments, or
  similar suppression unless the user explicitly approves it.
- Fix new findings in touched code. Report unrelated existing findings rather
  than broadening the task without authorization.
- Clean up temporary byproducts created during the task. Preserve intended
  generated artifacts and mention them in the result.
- Follow the 120-character Markdown limit configured in
  `.markdownlint.json`; prefer shorter lines when they remain readable.
- Follow `.agents/rules/markdown.md` for Markdown and
  `.agents/rules/toml.md` for TOML.

Once Go code exists, run the applicable checks while iterating on a change:

1. `gofmt` on changed Go files.
2. `go test ./...`.
3. `go vet ./...`.
4. `golangci-lint run ./...`.
5. `go test -race ./...` when concurrency-sensitive behavior changes and the
   environment supports it.

Do not run overlapping Go build, test, vet, or lint commands against the same
module. If a required tool or module does not exist yet, state that verification
was not applicable rather than inventing project setup.

### Full project check

`./run-all-tests.sh` is the complete test suite and the authoritative gate. The
Go checks above are the fast subset for iterating; they are not sufficient on
their own, because the script also enforces checks that no Go command covers.
Run the full script before reporting work complete whenever a change touches Go
sources, Markdown, the OpenAPI description, the shell scripts, or dependencies.
The repository has no CI, so nothing else runs these checks. Skipping the script
is how the duplication gate stayed red across four consecutive stories.

The script runs these components in order:

1. `gofmt -l` over tracked and untracked Go files.
2. `go test ./...`.
3. `go vet ./...`.
4. `golangci-lint run ./...`.
5. `go test -race ./...`.
6. A duplication marker balance check over Go files. An unbalanced
   `jscpd:ignore-start` suppresses duplication detection to the end of that
   file while still exiting successfully, so the imbalance has to fail before
   the scan reports a clean result.
7. JSCPD duplication detection over Go and markup sources at `--threshold 0`.
   Follow `.agents/rules/duplication.md` when it reports a clone.
8. `govulncheck ./...`, only when that tool is installed.
9. `trivy fs .` for dependency vulnerabilities and committed secrets.
10. Redocly lint of `api-doc/openapi.yaml`.
11. `markdownlint` over tracked Markdown outside `_bmad`, `_bmad-output`,
    `.agents/skills`, `.opencode`, `.cache`, and `vendor`.

The script includes the individual Go checks, so do not repeat them afterwards.
Several components download tooling through `npx` and need network access. When
a component cannot run, name it and say it was skipped instead of implying the
full suite passed.

## More rules

Read and implement all rules from `.agents/rules/*.md`
