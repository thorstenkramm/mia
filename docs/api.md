# API Design

Audience: frontend and backend developers. This document defines the current API
layout draft. The [product requirements](product-requirements.md) remain
authoritative for behavior, authorization, and security boundaries.

The API is not implemented yet. Attribute schemas and the open transport
decisions at the end of this document must be completed before implementation.

## Contents

- [Conventions](#conventions)
- [Frontend and local commands](#frontend-and-local-commands)
- [Authentication and recovery](#authentication-and-recovery)
- [Invitations](#invitations)
- [Current user and MFA](#current-user-and-mfa)
- [User administration](#user-administration)
- [Courses and relationships](#courses-and-relationships)
- [Materials and files](#materials-and-files)
- [Tutoring sessions and messages](#tutoring-sessions-and-messages)
- [Generated speech](#generated-speech)
- [Mentoring](#mentoring)
- [Jobs and audit](#jobs-and-audit)
- [Open decisions](#open-decisions)

## Conventions

### Prefix and media types

- Versioned API routes use `/api/v1`.
- JSON resources and errors use JSON:API with
  `application/vnd.api+json`.
- Resource types use plural kebab-case. Attributes use snake_case.
- Collections and resources have no trailing slash. The router does not serve
  the same resource at both forms.
- Uploads, downloads, generated audio, and tutor-response event streams use
  their explicitly documented non-JSON:API media types.
- A non-upload request body is limited to one MiB before parsing. Oversized
  requests return HTTP `413 Content Too Large`.

### Identifiers and time

- Resource IDs are opaque and clients never derive authorization from them.
- Every instant uses RFC 3339 UTC with the `Z` suffix.
- Responses and persistence normalize instants to exactly six fractional digits.
- Requests may contain at most nine fractional digits.
- Clients convert display-local values to UTC before sending an instant.
- One-time mentoring appointments use `scheduled_for` as a UTC instant.

### Optional text

- After field-specific trimming, empty or whitespace-only optional text is
  represented as JSON `null` and persisted as SQL null.
- Clients clear a writable optional text field with JSON `null` or an empty value.
- Required text fields reject empty or whitespace-only values.
- Except for passwords and chat messages, multiline profile and descriptive text
  is valid UTF-8. MIA normalizes CRLF and CR to LF, trims surrounding Unicode
  whitespace, and rejects NUL and controls other than tab and newline.

### Pagination and filtering

- Collection routes use offset pagination with `page[limit]` and
  `page[offset]`.
- The default limit is 25, the maximum limit is 100, and the default offset is
  zero. MIA rejects a negative value or an offset above 10,000.
- Unless a route documents a more meaningful fixed order, collections sort by
  `created_at` descending and then resource ID descending.
- Collections fetch one row beyond the requested limit to determine whether a
  next page exists. Responses provide offset, limit, and navigation links but do
  not calculate or return an exact total by default.
- Each collection documents its supported filters and stable sort order before
  implementation.
- Limits are bounded by MIA; clients cannot request unbounded collections.

### Included relationships

- No relationship is included by default.
- Each route explicitly allowlists includable direct relationships.
- A request may include at most three relationships. Nested include paths are not
  supported in the MVP.
- Unsupported, nested, duplicate, or excessive includes are validation errors;
  MIA does not silently ignore them.

### Errors and resource hiding

- Errors use JSON:API error objects with HTTP status, stable machine-readable
  code, title, and safe detail.
- Validation errors identify the relevant source pointer or parameter.
- Authentication-sensitive public responses do not reveal account existence.
- An out-of-scope sensitive resource returns the same public result as an
  unknown resource.
- Rate-limited responses use HTTP `429 Too Many Requests` and include
  `Retry-After` when known. Public password-recovery requests instead retain the
  same success response used for absent accounts and accepted delivery.
- Authentication-sensitive limit responses do not identify whether the IP,
  account, submitted identifier, token, or challenge caused the limit.
- Progressive login backoff returns immediately with `429`; handlers never sleep
  to enforce a retry delay.
- Unsupported sparse fieldsets, client-selected sorting, unlisted filters, and
  unknown query parameters return a stable validation error. MIA does not ignore
  them.
- Hidden-resource responses retain ordinary caller and IP limiting but create no
  limiter key derived from an unauthorized resource. Denied reads are not
  individually audited.

### Authorization

Every operation authorizes the action, role, course assignment, student
assignment, ownership, and resource state. Route grouping and possession of an
ID are never sufficient authorization.

### Browser session and CSRF

The MVP uses Echo session middleware with Gorilla `CookieStore`. The signed and
encrypted `__Host-mia_session` cookie contains only the user ID, login stage,
stage-specific challenge ID when needed, security generation, authentication
time, idle expiry, and absolute expiry. It is `Secure`, `HttpOnly`,
`SameSite=Lax`, has path `/`, and has no `Domain` attribute.

Every authenticated request reloads current account, role, assignment, ban, and
password-gate state from SQLite. Authorization never trusts those values from the
cookie. A cookie whose security generation differs from the current user row is
cleared and rejected. Each successful authenticated request reissues the cookie
with a 30-minute idle expiry capped by the original 12-hour absolute expiry. An
SSE connection refreshes the cookie when established; server-sent events do not.

MIA uses Echo's CSRF middleware with Fetch Metadata checks and token validation
for unsafe methods. The `__Host-mia_csrf` cookie is `Secure`, `SameSite=Lax`,
host-only, uses path `/`, and is readable by the frontend rather than
`HttpOnly`. The frontend sends its value in `X-CSRF-Token` when token validation
is required. MIA supports same-origin browser access only in the MVP and does not
enable CORS.

`GET /api/v1/auth/session` always issues or refreshes anonymous CSRF state. Every
unsafe public endpoint, including login, invitation preview and acceptance, and
recovery, requires the matching cookie and header. Cross-site Fetch Metadata is
rejected; missing Fetch Metadata is accepted only with a valid CSRF token. MIA
rotates CSRF state after completed login, logout, and every login-stage
transition, invalidating the old value immediately.

## Frontend and local commands

MIA serves regular frontend files for GET and HEAD outside `/api`, rejects
symlink escapes, dotfiles, and directory listings, and uses `index.html` fallback
only for unresolved non-API GET or HEAD routes. Unknown API paths never fall back
to frontend HTML. Static responses use `X-Content-Type-Options: nosniff`, a
restrictive referrer policy, frame denial, and a strict baseline CSP
(`default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors
'none'`), loosened during frontend integration only when the frontend
demonstrably requires it.

The first-administrator bootstrap has no HTTP route. With the server stopped, the
operator runs the interactive `mia bootstrap-admin` command locally. The command
requires a terminal and prompts for username, email, language, country, time
zone, password, and password confirmation. It refuses redirected input and never
accepts account fields through arguments, environment variables, or input files.

The command validates the normal account and password rules, verifies that no
administrator exists inside the bootstrap transaction, creates the user and
administrator role, records the local-operator audit event, and commits. It makes
no database change if validation fails, an administrator already exists, or any
write fails. The supplied administrator email is marked verified because the
local operator creates the account directly.

Sole-administrator MFA recovery also has no HTTP route. With the server stopped,
the operator runs the interactive `mia reset-admin-mfa` command locally. The
command refuses unless exactly one administrator exists and that account has MFA
state to reset. It displays the identified administrator and requires the
operator to type its exact displayed username before making changes.

`mia serve` and both local commands acquire the exclusive
`main.data_dir/mia.lock` OS lock before opening SQLite. Local commands refuse
while the server or another database-using command holds it.

Inside one transaction, the command rechecks that exactly one administrator
exists, ends active and pending factors, invalidates recovery codes and MFA
challenges, requires password replacement, and records the local-operator audit
event. It makes no database change if confirmation is withheld, a precondition
fails, or any write fails. Current browser cookies become restricted to password
replacement and logout when MIA next checks account state.

## Authentication and recovery

- `POST /api/v1/auth/login`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/session`
- `POST /api/v1/auth/password-changes`
- `POST /api/v1/auth/password-recovery-requests`
- `POST /api/v1/auth/password-resets`
- `POST /api/v1/auth/mfa-challenges/{id}/verifications`
- `POST /api/v1/auth/mfa-challenges/{id}/resends`
- `POST /api/v1/auth/mfa-challenges/{id}/recovery-code-consumptions`
- `POST /api/v1/auth/mfa-management-proofs`

### First password-authentication slice

`POST /api/v1/auth/login` accepts a `login-attempts` JSON:API resource with `username` and `password`
attributes. A successful response is `200` and contains an `auth-sessions` resource whose ID is the authenticated
user ID and whose attributes contain only `stage` (`authenticated`, `mfa`, or `password-change`). An `mfa` response
also contains the opaque `mfa_challenge_id`. It never returns credentials, password hashes, roles, or profile data.
Invalid credentials and banned accounts both return `401 auth_invalid_credentials`; invalid request shape returns
`422 auth_invalid_request`; a body exceeding 1 MiB returns `413 auth_request_too_large`; an unsupported request
media type returns `415 auth_unsupported_media_type`; throttling returns `429 auth_login_throttled` with
`Retry-After`.

`POST /api/v1/auth/logout` requires a valid login-stage cookie, clears that browser's cookie, rotates CSRF state, and
returns `204`. A missing, invalid, or expired stage cookie returns `401 auth_unauthenticated`.

`POST /api/v1/auth/password-changes` requires the `password-change` stage and accepts a `password-changes`
resource with `password` and `password_confirmation` attributes. It validates equality and normal password policy,
clears the password gate, rotates into a fresh authenticated session and CSRF state, and returns the safe
`auth-sessions` resource. A missing or different stage returns `403 auth_password_change_required`; invalid or
non-compliant input returns `422 auth_invalid_password` without echoing the submitted value.

`POST /api/v1/auth/password-recovery-requests` accepts a `password-recovery-requests` resource with the complete
`username` attribute. It always returns `204` for a well-formed request, including for unknown, banned, student-only,
syntactically invalid usernames, and rate-limited requests; it does not send `Retry-After`. An invalid resource shape
returns `422 auth_invalid_request`, oversized input returns `413 auth_request_too_large`, and an unsupported media type
returns `415 auth_unsupported_media_type`.

`POST /api/v1/auth/password-resets` accepts a `password-resets` resource with `token`, `password`, and
`password_confirmation` attributes. It returns `204` after a valid reset and never creates a browser session. Unknown,
expired, consumed, malformed, banned, or deleted-token owners return `422 auth_invalid_reset_token` uniformly; throttled
submissions return the same error without `Retry-After`. A valid token with non-compliant or mismatched passwords
returns `422 auth_invalid_password` and leaves the token usable; request shape, body size, and media type errors use the
same codes as recovery requests.

### Password blocklist provenance

MIA embeds `Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt` from
`danielmiessler/SecLists` commit `f025490a4bc7bd1d6cd36c3b834631acd615ff28` (source timestamp
`2026-08-30T11:13:41Z`). The embedded snapshot is upstream MIT-licensed. It is compared against valid submitted
password bytes exactly; MIA does not trim, normalize, case-fold, or generate password mutations.

Login and password recovery accept a complete username only. Login may return an
MFA challenge instead of a complete authenticated session. Public recovery
responses are account-enumeration safe, including for banned accounts. Banned
accounts cannot request or complete recovery. A new request does not invalidate
earlier links; successful reset invalidates every remaining challenge. Successful
reset does not revoke other stateless browser cookies in the MVP. A reset link
expires after 30 minutes, is single-use, and uses
`/password-reset#token=<uuid>`. The frontend removes the fragment before its API
request.

Login cookie stages are:

- `mfa`, which permits only verification or recovery-code consumption for the
  matching challenge, resend when that challenge uses SMS, and logout;
- `password-change`, which permits only password replacement and logout;
- `authenticated`, which permits normal authenticated routes subject to
  authorization.

Restricted stages expire after 30 minutes and do not refresh. Password
verification creates `mfa` before `password-change` when both are required. MIA
rotates the cookie after MFA, password replacement, and completed login. Login
MFA verification, recovery-code consumption, and SMS resend require both the
matching challenge ID and the bound `mfa` cookie stage; a challenge ID alone
grants no authority. Sensitive-action verification instead requires a fully
authenticated cookie for the same user.

MFA enrollment expires after 30 minutes, and five incorrect submissions delete
the pending enrollment. A pending SMS enrollment resend sends the existing code
without extending expiry or resetting failures. TOTP provisioning uses issuer
`MIA (<main.public_url hostname>)` and the username as account label. Successful
activation returns ten recovery codes once. Disabling or replacing active MFA
requires the current password and a fresh current-factor or recovery-code proof.
POST enrollment creates a pending replacement when an active factor exists.
Replacement activation atomically consumes the password and MFA-management proof.
DELETE cancels a pending enrollment or, with the same proof requirements,
disables an active factor. If an activation response containing recovery codes is
lost, the codes cannot be retrieved; complete factor replacement is the recovery
path. An active SMS factor retains its enrolled destination when the profile
mobile changes. Successful profile-mobile change or removal deletes pending SMS
factors.
Login MFA challenges also expire after 30 non-refreshing minutes. Separate login
attempts may have concurrent challenges, and completion consumes only the one
used. A TOTP time step can succeed only once per factor. Login SMS challenge
resend has the same preservation behavior.

Successful current-factor confirmation returns one opaque MFA-management proof.
It is valid for five minutes and one MFA disable or replacement. The mutation
consumes it atomically. Password, MFA, ban-state, or account-state changes
invalidate all outstanding MFA challenges and management proofs for the user.

### MFA request resources

`POST /api/v1/users/me/mfa-enrollments` accepts an `mfa-enrollments` resource
with `method`. TOTP enrollment returns an `mfa-enrollments` resource containing
the provisioning URI. SMS enrollment is unavailable until ClickSend and verified
profile-mobile support are configured. Verification accepts an
`mfa-enrollment-verifications` resource with `code`; success returns exactly ten
`mfa-recovery-codes` once. Codes are 16-character uppercase Crockford Base32
values and cannot be retrieved again.

MFA challenge verification accepts `mfa-verifications` with `code`; recovery
consumption accepts `mfa-recovery-code-consumptions` with `code`. Both require a
matching `mfa` session cookie and challenge ID. Invalid values return
`auth_invalid_mfa_code` or `auth_invalid_recovery_code`; replayed TOTP steps
return `auth_mfa_step_used`.

`POST /api/v1/auth/mfa-management-proofs` accepts an
`mfa-management-proofs` resource with the current password and MFA `code`. It
returns an opaque UUID proof. Supplying that proof in `X-MFA-Management-Proof`
allows one factor disable through `DELETE /api/v1/users/me/mfa-enrollments/{id}`
or one replacement enrollment. A missing, expired, or consumed proof returns
`auth_mfa_proof_required`.

## Invitations

Authenticated invitation management uses stable invitation IDs:

- `GET|POST /api/v1/invitations`
- `GET|DELETE /api/v1/invitations/{id}`
- `POST /api/v1/invitations/{id}/resends`

Public preview and acceptance submit the invitation token in the request body so
tokens do not appear in access-log paths:

- `POST /api/v1/invitation-previews`
- `POST /api/v1/invitation-acceptances`

Administrator, supervisor, and mentor invitations are single-use, do not expire,
and normally remain pending until accepted or revoked. Definite initial SMTP
failure makes one faulty and unusable; an SMTP timeout leaves it pending and
usable, logs a sanitized error, and is not retried. Authorized reads expose the
state and sanitized failure code. Faulty invitations can only be deleted. To
token holders, preview and acceptance treat revoked, faulty, accepted, and
unknown tokens identically with one generic invalid-invitation response. Links
use `/invitation#token=<uuid>` and the frontend removes the fragment before its
API request. Student accounts do not use invitations. Invitations contain no
course or student scope. Acceptance always creates a new account and its invited
permanent role. Creating or accepting an invitation whose email is already used
by a registered account is rejected; role changes use the user-ID operation
below. Any supervisor may create a mentor invitation; only its inviting
supervisor or an administrator may view, resend, revoke, or delete it.
Supervisor invitation acceptance atomically creates both supervisor and student
roles.

`DELETE /invitations/{id}` is state-dependent. For a pending invitation it
atomically revokes the invitation, clears its token, records revocation
attribution, and writes the revocation audit event. For a faulty invitation it
physically deletes the row and writes a content-free deletion audit event.
Accepted and already revoked invitations reject DELETE without changing state.
The same actor authorization governs pending revocation and faulty deletion.

## Current user and MFA

- `GET|PATCH /api/v1/users/me`
- `GET|POST /api/v1/users/me/mfa-enrollments`
- `POST /api/v1/users/me/mfa-enrollments/{id}/verifications`
- `POST /api/v1/users/me/mfa-enrollments/{id}/resends`
- `DELETE /api/v1/users/me/mfa-enrollments/{id}`
- `POST /api/v1/users/me/mobile-change-challenges`
- `POST /api/v1/users/me/mobile-change-challenges/{id}/verifications`
- `POST /api/v1/users/me/mobile-change-challenges/{id}/resends`
- `DELETE /api/v1/users/me/mobile`
- `GET|PUT|DELETE /api/v1/users/me/avatar`

Field-level authorization still applies to `PATCH /users/me`. Student-only
accounts cannot mutate their profiles. Administrators, supervisors, and mentors
may change their own name, nickname, language, country, time zone, avatar, and TTS
voice. Mobile-change challenge routes manage their verified mobile number.
Username, email, roles, ban state, password state, and security fields remain
outside generic profile PATCH.

DELETE on the current user's mobile clears the verified profile number and
pending mobile-verification challenges and pending SMS factors without SMS
delivery. It leaves an active SMS MFA factor's enrolled destination unchanged and
writes a content-free audit event. New SMS enrollment requires a verified profile
mobile; login challenges use the active factor's destination snapshot. MIA
exposes no standalone recovery-code regeneration route; replacing the MFA factor
invalidates its old codes and returns the new factor's codes once.

Avatar upload accepts bounded JPEG or PNG rather than JSON:API. The source is at
most 10 MiB, 40 decoded megapixels, and 10,000 pixels per dimension. MIA applies
orientation, strips metadata, fits the image within 512 by 512 pixels without
changing its aspect ratio, and stores PNG.

Avatar download is represented by an authorized URL in the user resource; it
never exposes an internal filesystem path. Download authorization is identical
to profile-view authorization for that user and responses use `image/png`, safe
content headers, a strong content ETag, and `Cache-Control: private, no-cache`.

The implemented current-user resource has type `users`. `GET /users/me` returns
`username`, nullable `email`, nullable `name`, nullable `nickname`,
`preferred_language`, `country`, `time_zone`, nullable `tts_voice`,
`has_verified_mobile`, and nullable `avatar_url`. It deliberately does not return
the mobile number. `PATCH /users/me` accepts only `name`, `nickname`,
`preferred_language`, `country`, `time_zone`, and `tts_voice`; unknown or
security-owned fields reject the whole request. It returns the updated resource.

Creating a mobile challenge accepts a `mobile-change-challenges` resource with
one `mobile` attribute and returns only its challenge ID. Verification accepts a
`mobile-change-verifications` resource with one `code` attribute. Resend and
verification success return 204 and never return a destination or code. Mobile
removal returns 204. Profile and mobile mutations use JSON:API; avatar PUT uses
exactly `image/jpeg` or `image/png` and successful avatar PUT/DELETE return 204.

## User administration

- `GET /api/v1/users`
- `GET|PATCH|DELETE /api/v1/users/{id}`
- `POST /api/v1/users/{id}/roles`
- `GET|PUT|DELETE /api/v1/users/{id}/avatar`
- `POST /api/v1/users/{id}/temporary-passwords`
- `POST /api/v1/users/{id}/mfa-resets`
- `POST /api/v1/users/{id}/bans`
- `DELETE /api/v1/users/{id}/bans`

These routes do not create a generic administrator override. Each operation
enforces its role, course, student, and protected-field rules. Student
provisioning and course membership use the course routes below.

`POST /api/v1/users/{id}/mfa-resets` is deferred until story 1-8 supplies the
course-membership authorization required for student-only and staff reset paths.

Every user can retrieve self. Assigned supervisors can list students and staff
relationships in assigned courses. Mentors receive only minimal identity —
username, name, nickname, and avatar — for assigned students. The student role alone grants no roster. Administrators can
list account and relationship metadata needed for administration, not private
course content.

Creating an invitation with role `administrator` requires an administrator.
Direct role assignment identifies a registered staff account by user ID, applies
immediately, and requires no affected-user approval. The target must already have
a staff role and verified email; student-only accounts cannot be promoted. New
staff accounts use invitations. An administrator may grant the administrator or
supervisor role; any supervisor may grant the mentor role. Granting supervisor
atomically grants student when absent. Banned targets are rejected until
explicitly unbanned. Repeated assignment is idempotent. Administrator, supervisor,
and mentor roles cannot be removed.
`DELETE /users/{id}` requires an administrator for a student-only account and a
different administrator for any account with a staff role. Staff deletion rejects
the last administrator and a sole supervisor of any course. Otherwise it removes
current assignments, triages affected open mentoring work, deletes the complete
account and student-owned data, and clears retained historical actor references
in one transaction.

An assigned supervisor may manage only a student-only account through
`PATCH /users/{id}`, including setting or clearing its immediately verified mobile
number. The transaction invalidates pending mobile challenges and leaves an
active SMS factor at its enrolled destination. Ban routes apply only to
student-only accounts. A ban rejects the next request but does not cancel work
already in flight. Student-only and staff account deletion do not coordinate with
running provider calls. Late writes update existing rows only; a zero-row guarded
update discards the result, removes newly published output, closes synchronized
response subscribers, and never recreates or requeues deleted state.
Setting a student-only account's temporary password or resetting its MFA
increments the user's security generation in the same transaction. This
invalidates every existing cookie for the student. Staff accounts always use the
staff recovery paths. A fresh student login enters the `password-change` stage
after password verification and any required MFA stage.

## Courses and relationships

- `GET|POST /api/v1/courses`
- `GET|PATCH|DELETE /api/v1/courses/{id}`
- `GET|PUT|DELETE /api/v1/courses/{id}/logo`
- `POST /api/v1/courses/{id}/activations`
- `POST /api/v1/courses/{id}/deactivations`
- `GET|POST /api/v1/courses/{id}/supervisors`
- `DELETE /api/v1/courses/{id}/supervisors/{user_id}`
- `GET|POST /api/v1/courses/{id}/students`
- `DELETE /api/v1/courses/{id}/students/{user_id}`
- `GET|POST /api/v1/courses/{id}/mentors`
- `DELETE /api/v1/courses/{id}/mentors/{user_id}`
- `GET|POST /api/v1/courses/{id}/students/{student_id}/mentors`
- `DELETE /api/v1/courses/{id}/students/{student_id}/mentors/{mentor_id}`

`POST /courses` requires one or more initial supervisor relationships. Every
referenced user must already hold the supervisor role. MIA validates the complete
request and creates the inactive course and all initial assignments in one
transaction; any invalid relationship or failed write rolls back the operation.
The supervisors collection adds only later assignments.

Adding a student accepts either an existing student relationship or the fields
needed for supervisor provisioning. It requires an active course and is not a
public registration workflow.

The request explicitly selects new-account or existing-account mode.
Existing-account mode accepts only the complete username, performs no search,
requires an existing student role, and rejects profile or password fields.
Unknown and non-student usernames return the same safe not-found response. An
existing membership returns that membership idempotently. A clean rejoin creates
a new membership and restores no deleted course data.

Only an administrator removes a course supervisor, and the last supervisor
cannot be removed. An assigned supervisor may remove a student only when that
student has no active tutoring session in the course. Removal atomically deletes
all data owned by that student in the course while preserving the account and
other-course data. It does not wait for running workers or provider calls; late
results follow the shared zero-row stale-result rule.

An administrator or assigned supervisor may mutate the course logo. GET uses the
same authorization as viewing the course and returns the normalized PNG with a
strong content ETag and `Cache-Control: private, no-cache`. Upload limits and
normalization match avatars.

Mentor removal drops affected student assignments and returns open mentoring work
to supervisor triage in one transaction. Course deletion remains restricted to
an inactive course with no active tutoring sessions. Once allowed, deletion does
not wait for running workers or provider calls; late results follow the shared
zero-row stale-result rule.

`POST /courses/{id}/mentors` assigns an existing registered mentor to the course.
An assigned supervisor performs the operation, and it takes effect immediately
without mentor acceptance. A mentor cannot reject or remove the course
assignment. Student mentor routes similarly create and remove immediate
supervisor-controlled assignments, and the selected mentor must already belong
to the course.

## Materials and files

- `GET|POST /api/v1/courses/{course_id}/materials`
- `GET|PATCH|DELETE /api/v1/materials/{id}`
- `POST /api/v1/materials/{id}/finalizations`
- `POST /api/v1/materials/{id}/approvals`
- `DELETE /api/v1/materials/{id}/approvals`
- `GET|POST /api/v1/materials/{id}/files`
- `GET|DELETE /api/v1/material-files/{id}`
- `GET /api/v1/material-files/{id}/download`
- `GET /api/v1/material-files/{id}/content`

File upload uses bounded `multipart/form-data`. MIA validates signatures and
detected media types into a temporary file, atomically renames the validated file
to its deterministic source path, then inserts and commits the file row. A failed
transaction removes the published file, and startup removes source files without
rows. A successful response means publication and row commit both succeeded.
Download responses use the safe media type derived from the material format,
safe content disposition, and `X-Content-Type-Options: nosniff`.

`content` streams authorized normalized `content.jsonl` as
`application/x-ndjson`; it does not construct a large JSON:API document. Every
line contains `version: 1`, positive contiguous `sequence`, nullable
`chapter_label`, nullable `section_label`, and `text`. It never returns an
internal path or raw OCR provider response.

Course-wide approval is separate from upload and processing. Student-private
material cannot be approved or converted to course-wide material. Changing the
validated brief content of approved course-wide material atomically revokes
approval, clears its attribution, and writes content-free correction and
revocation audit effects; an unchanged brief does not.

Files may change only while a material is draft or retryable failed. In one
transaction, finalization validates and freezes the complete file set, changes
every file and the material to processing, and inserts exactly one extraction job
per file. Any failure rolls back all changes; a uniqueness constraint prevents
duplicate active work. Ready material is immutable and must be deleted and
recreated to change its source. Approval requires ready state and a non-empty
brief; link-only material does not satisfy course readiness.

Link-only course-wide finalization instead validates the URL, absence of files,
and non-empty supervisor-authored brief and changes draft directly to ready in one
transaction without creating extraction or summary jobs.

The finalization route also retries a failed material after automatic attempts
are exhausted, with or without file changes. MIA has no generic job retry route.
Material brief responses use the strict version-1 schema documented in the
product requirements. External URL, original-filename, page-count, PDF, DOCX,
normalized-content, and summary-capacity rules apply before successful state is
reported. File-backed material reports ready only after the summary result
transaction has stored a valid brief, marked its job succeeded, and verified
every file processed.

Material deletion is rejected while the material is selected by an active
tutoring session. It does not otherwise wait for running workers or provider
calls. After session completion, normal deletion rules apply, and stale late
results cannot recreate deleted state.

## Tutoring sessions and messages

- `GET|POST /api/v1/courses/{course_id}/tutoring-sessions`
- `GET|PATCH /api/v1/tutoring-sessions/{id}`
- `POST /api/v1/tutoring-sessions/{id}/completion`
- `POST /api/v1/tutoring-sessions/{id}/summary-generations`
- `GET|POST /api/v1/tutoring-sessions/{id}/messages`
- `POST /api/v1/student-messages/{id}/response-retries`
- `POST /api/v1/tutor-responses/{id}/interruptions`
- `GET /api/v1/tutor-responses/{id}/events`
- `GET /api/v1/tutoring-sessions/{id}/materials`

Session creation requires a canonical lowercase UUID v4 `client_request_id` and
optionally includes selected material relationships. While the resulting session
is active, replaying the same ID and creation payload returns it; different
content is a conflict. After completion, every reuse of that request ID is a
conflict while the session remains retained. Authorized deletion removes its
request-ID history; MIA stores no tombstone. The owning student can complete an
active session; no abandon or staff force-completion route exists.

Selected source material must belong to the session's course and be ready and
file-backed. Course-wide selections also require approval; private selections
require ownership by the active student and no approval. Link-only material may
supply authorized identity and brief context but no retrievable source content.

PATCH permits only an assigned supervisor to update `summary` and `follow_up` on
a completed session. The transaction records supervisor summary attribution and
exactly one content-free correction audit event. Every other session field and
the completed chat remain immutable.

Each student-message creation includes a canonical lowercase UUID v4 request ID
scoped to the session. Repeating the same ID and content returns the existing
message and response operation even after completion; this replay is read-only.
Different content is a conflict. A request that would create a new message
requires an active session. Authorized account, course, or membership deletion
may cascade the owning session and removes this request-ID history without a
tombstone.

A session accepts at most one generating response and one queued message. A
further message returns a conflict. Queued work begins after any terminal result
from current generation. Completion returns a conflict while work is queued or
generating. Response retry is available only when the session is idle.
Completed sessions reject new message creation and response retry but still
return retained identical message replays.

An assigned supervisor may request summary generation manually only for a
completed session with no summary, no queued or running summary job, and at least
one earlier terminally failed summary job.

Assigned supervisors can see active-session status — including the session's
start and last-activity instants — but cannot retrieve messages until
completion. Material retrieval performed by the AI tutor uses internal
authorized application tools, not client-selected arbitrary file paths.
Retrievable content belongs to the session's course and is ready and file-backed.
Course-wide content also requires approval; student-private content requires
ownership by the session student and no approval. Course deactivation does not
remove otherwise authorized content from an existing session.

### Tutor-response events

`GET /api/v1/tutor-responses/{id}/events` returns a Server-Sent Events stream
with media type `text/event-stream`. Establishing a stream reauthorizes access to
the response. The stream exposes MIA events only; raw OpenAI events, tool calls,
provider errors, and provider payloads are never forwarded.

On every connection, MIA first sends the current content and state:

```text
event: snapshot
data: {"content":"Current persisted text","state":"generating"}
```

Generation start, new text, and terminal state changes use these events:

```text
event: started
data: {"state":"generating"}

event: delta
data: {"text":"next text"}

event: completed
data: {"state":"completed"}

event: interrupted
data: {"state":"interrupted"}

event: failed
data: {"state":"failed","code":"provider_failure"}
```

MIA sends an SSE comment heartbeat every 15 seconds while no event is available.
Each stream write has a 30-second deadline. A browser disconnect closes only that
subscription; it does not cancel response generation. Reconnecting to the same
route receives a fresh snapshot followed by new deltas, so MIA does not need a
persisted per-token event history.

MIA registers the subscriber and captures the current content and state as one
per-response synchronized operation. Deltas created after that point enter the
subscriber queue, preventing a gap between snapshot and live delivery without a
persisted SSE event log. During generation the synchronized in-memory content may
be ahead of the latest bounded persistence batch; persisted content remains the
restart-recovery baseline.

If a bounded persistence update affects zero rows, MIA marks the synchronized
in-memory response stale, closes all subscriber queues, and discards every later
provider event without delivering or persisting it. Deletion alone does not
cancel the provider request; it may finish or reach its existing deadline. MIA
does not add separate cross-goroutine deletion signaling or recreate deleted
state.

Each subscriber queue holds at most 64 events or 256 KiB, whichever is reached
first. Overflow closes only that SSE connection; it never blocks provider
consumption or other subscribers. Reconnection recovers through a fresh snapshot.

The reverse proxy must not buffer this route. MIA flushes complete SSE events and
persists generated text after 16 KiB of new UTF-8 output or one second, whichever
occurs first, and before committing a terminal state.

The interruption route accepts queued and generating responses. A queued
response becomes interrupted without a provider call; a generating response
cancels its provider operation. Startup resumes queued work and marks stranded
generation failed rather than recreating an uncertain provider request.
If cancellation cannot be confirmed, MIA retains the interrupted state, discards
late events, and logs a sanitized failure.

## Generated speech

- `POST /api/v1/tutor-responses/{id}/speech`
- `GET /api/v1/tutor-responses/{id}/speech`

The POST route requests or reuses generation for the user's selected voice. The
GET route returns JSON:API state while generation is pending or failed and audio
with its documented media type when available. Every request reauthorizes access
to the source response.

Only completed tutor responses are eligible. Available audio is MP3 served as
`audio/mpeg`. Concurrent POST requests reuse one cache record and provider
operation. Retrying a failed variant transitions the same record back to
generating; request-path generation never retries automatically.

When ElevenLabs is not configured, both routes return a stable
feature-unavailable error.
Generation also requires a stored voice ID of at most 128 printable ASCII
characters; absence returns a stable validation error. MP3 output is limited to
25 MiB and is signature-validated before atomic publication.

At startup, after expired-speech cleanup, every cache row stranded in generating
state becomes failed with a sanitized restart code and loses any partial or
published MP3. MIA does not repeat the uncertain ElevenLabs request. A later
authorized POST retries the same row through the normal failed-to-generating
transition.

## Mentoring

One resource represents the request, mentor response, optional appointment, and
closure:

- `GET|POST /api/v1/courses/{course_id}/mentoring-sessions`
- `GET|PATCH /api/v1/mentoring-sessions/{id}`

Creation is unassigned and may include `proposed_for`. An assigned course
supervisor selects one of the student's assigned mentors through `PATCH`. The
assigned mentor may then respond and set `scheduled_for`. The student or
supervisor may cancel an unscheduled request; the student or assigned mentor may
cancel a future schedule; only the assigned mentor may complete it after the
scheduled time.

An assigned supervisor may directly replace the current mentor on an open row.
The update preserves proposed and scheduled times, meeting details, prior
response, and immutable response authorship. Mentor removal remains a separate
operation that clears future schedule details and returns open work to triage.

Course deactivation blocks creation but not existing work. Mentor removal clears
the current mentor, proposed and scheduled times, and meeting details on open work
and returns it to supervisor triage.

MIA provides no live mentoring channel. Meeting URLs are HTTPS metadata and are
never fetched by MIA.

## Jobs and audit

- `GET /api/v1/jobs`
- `GET /api/v1/jobs/{id}`
- `GET /api/v1/materials/{id}/jobs`
- `GET /api/v1/tutoring-sessions/{id}/jobs`
- `GET /api/v1/audit-events`

Job usage fields are unattributed, model-agnostic cumulative counters across
attempts. They may span configuration changes and are operational information,
not model-specific attribution or authoritative billing totals.

Administrators can inspect platform jobs. Supervisors receive only the safe
processing status needed for resources in their assigned courses; they do not
receive unrestricted provider diagnostics.

Audit access is administrator-only. Filters and output fields are allowlisted,
and audit resources never expose secret or content-bearing values.

The first version has no manual job retry route. MIA performs only its bounded
automatic retries.

## Open decisions

Authentication and session routes will be the first complete vertical slice:
anonymous session and CSRF, login, logout, password change, recovery, MFA
challenges, and MFA-management proof.

Each route family still needs exact attributes, writable fields, relationships,
includes, filters, ordering, status codes, stable errors, authorization,
redaction, idempotent replay behavior, and field-level text and collection bounds
before implementation:

- authentication, session, recovery, and MFA;
- invitations and invitation acceptance;
- user administration, roles, bans, and deletion;
- courses, logos, supervisors, students, course mentors, and student mentor
  assignments;
- materials, source files, finalization, briefs, approval, and content;
- tutoring sessions, messages, responses, SSE, retrievals, and summaries;
- generated speech;
- mentoring requests, triage, responses, scheduling, and closure;
- jobs and audit events.

Resolve each item in this document or a more specific current architecture
document before implementing the affected routes. Superseded proposals should be
removed rather than retained as history.
