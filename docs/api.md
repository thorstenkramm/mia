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
- Clients convert display-local values to UTC before sending an instant.
- One-time mentoring appointments use `scheduled_for` as a UTC instant.

### Optional text

- After field-specific trimming, empty or whitespace-only optional text is
  represented as JSON `null` and persisted as SQL null.
- Clients clear a writable optional text field with JSON `null` or an empty value.
- Required text fields reject empty or whitespace-only values.

### Pagination and filtering

- Collection routes use offset pagination with `page[limit]` and
  `page[offset]`.
- Each collection documents its supported filters and stable sort order before
  implementation.
- Limits are bounded by MIA; clients cannot request unbounded collections.

### Errors and resource hiding

- Errors use JSON:API error objects with HTTP status, stable machine-readable
  code, title, and safe detail.
- Validation errors identify the relevant source pointer or parameter.
- Authentication-sensitive public responses do not reveal account existence.
- An out-of-scope sensitive resource returns the same public result as an
  unknown resource.
- Rate-limited responses use HTTP `429 Too Many Requests` and include
  `Retry-After` when known.
- Authentication-sensitive limit responses do not identify whether the IP,
  account, submitted identifier, token, or challenge caused the limit.
- Progressive login backoff returns immediately with `429`; handlers never sleep
  to enforce a retry delay.

### Authorization

Every operation authorizes the action, role, course assignment, student
assignment, ownership, and resource state. Route grouping and possession of an
ID are never sufficient authorization.

### Browser session and CSRF

The MVP uses Echo session middleware with Gorilla `CookieStore`. The signed and
encrypted `__Host-mia_session` cookie contains only the user ID, login stage,
stage-specific challenge ID when needed, authentication time, idle expiry, and
absolute expiry. It is `Secure`, `HttpOnly`, `SameSite=Lax`, has path `/`, and
has no `Domain` attribute.

Every authenticated request reloads current account, role, assignment, ban, and
password-gate state from SQLite. Authorization never trusts those values from the
cookie. Each successful authenticated request reissues the cookie with a
30-minute idle expiry capped by the original 12-hour absolute expiry. An SSE
connection refreshes the cookie when established; server-sent events do not.

MIA uses Echo's CSRF middleware with Fetch Metadata checks and token validation
for unsafe methods. The `__Host-mia_csrf` cookie is `Secure`, `SameSite=Lax`,
host-only, uses path `/`, and is readable by the frontend rather than
`HttpOnly`. The frontend sends its value in `X-CSRF-Token` when token validation
is required. MIA supports same-origin browser access only in the MVP and does not
enable CORS.

## Frontend and local commands

MIA serves the separately installed frontend outside `/api`. Unknown API paths
never fall back to frontend HTML.

The first-administrator bootstrap has no HTTP route. With the server stopped, the
operator runs the interactive `mia bootstrap-admin` command locally. The command
collects the required account fields, reads the password twice from a terminal,
and refuses redirected password input. It never accepts a password through a
command-line argument, environment variable, or input file.

The command validates the normal account and password rules, verifies that no
administrator exists inside the bootstrap transaction, creates the user and
administrator role, records the local-operator audit event, and commits. It makes
no database change if validation fails, an administrator already exists, or any
write fails. The supplied administrator email is marked verified because the
local operator creates the account directly.

Sole-administrator MFA recovery also has no HTTP route. With the server stopped,
the operator runs the interactive `mia reset-admin-mfa` command locally. The
command refuses unless exactly one administrator exists and that account has MFA
state to reset. It displays the identified administrator and requires terminal
confirmation before making changes.

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

Login may return an MFA challenge instead of a complete authenticated session.
Public recovery responses are account-enumeration safe. Successful password
reset does not revoke other stateless browser cookies in the MVP. A password
reset link expires 30 minutes after issuance and is single-use. Its bearer token
is a canonical lowercase UUID v4.

Login cookie stages are:

- `mfa`, which permits only the matching MFA challenge verification or recovery
  code consumption and logout;
- `password-change`, which permits only password replacement and logout;
- `authenticated`, which permits normal authenticated routes subject to
  authorization.

Restricted stages expire after 30 minutes and do not refresh. Password
verification creates `mfa` before `password-change` when both are required. MIA
rotates the cookie after MFA, password replacement, and completed login. An MFA
verification route requires both the matching challenge ID and the bound `mfa`
cookie stage; a challenge ID alone grants no authority.

MFA enrollment expires after 30 minutes, and five incorrect submissions delete
the pending enrollment. TOTP provisioning uses issuer
`MIA (<main.public_url hostname>)` and the username as account label. Successful
activation returns ten recovery codes once. Disabling or replacing active MFA
requires the current password and a fresh current-factor or recovery-code proof.
An SMS factor retains its enrolled destination when the profile mobile changes.

## Invitations

Authenticated invitation management uses stable invitation IDs:

- `GET|POST /api/v1/invitations`
- `GET|DELETE /api/v1/invitations/{id}`
- `POST /api/v1/invitations/{id}/resends`

Public acceptance submits the invitation token in the request body so tokens do
not appear in access-log paths:

- `POST /api/v1/invitation-acceptances`

Administrator, supervisor, and mentor invitations are single-use, do not expire,
and remain pending until accepted or revoked. Their bearer tokens are canonical
lowercase UUID v4 values. Student accounts do not use invitations.

## Current user and MFA

- `GET|PATCH /api/v1/users/me`
- `GET|POST /api/v1/users/me/mfa-enrollments`
- `POST /api/v1/users/me/mfa-enrollments/{id}/verifications`
- `DELETE /api/v1/users/me/mfa-enrollments/{id}`
- `POST /api/v1/users/me/mobile-change-challenges`
- `POST /api/v1/users/me/mobile-change-challenges/{id}/verifications`
- `POST /api/v1/users/me/mobile-change-challenges/{id}/resends`
- `PUT|DELETE /api/v1/users/me/avatar`

Field-level authorization still applies to `PATCH /users/me`. In particular, a
student can change only the self-service fields confirmed by the product
requirements.

Avatar upload uses a bounded image media type rather than JSON:API. Avatar
download is represented by an authorized URL in the user resource; it never
exposes an internal filesystem path.

## User administration

- `GET /api/v1/users`
- `GET|PATCH|DELETE /api/v1/users/{id}`
- `PUT|DELETE /api/v1/users/{id}/avatar`
- `POST /api/v1/users/{id}/temporary-passwords`
- `POST /api/v1/users/{id}/mfa-resets`
- `DELETE /api/v1/users/{id}/roles/administrator`
- `POST /api/v1/users/{id}/bans`
- `DELETE /api/v1/users/{id}/bans`

These routes do not create a generic administrator override. Each operation
enforces its role, course, student, and protected-field rules. Student
provisioning and course membership use the course routes below.

Creating an invitation with role `administrator` requires an administrator.
Deleting an administrator role requires a different administrator and is denied
for the last administrator. `DELETE /users/{id}` requires an administrator for a
student-only account and a different administrator for any account with a staff
role. Protected course and mentor relationships must be resolved first.

## Courses and relationships

- `GET|POST /api/v1/courses`
- `GET|PATCH|DELETE /api/v1/courses/{id}`
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
other-course data.

Mentor removal accepts the required replacement mentor when open mentoring
sessions must be reassigned. Course deletion remains restricted to an inactive
course with no active tutoring sessions.

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
detected media types before retaining files. Download responses use the safe
media type derived from the material format, safe content disposition, and
`X-Content-Type-Options: nosniff`.

`content` returns authorized normalized extracted text. It never returns an
internal path or raw OCR provider response.

Course-wide approval is separate from upload and processing. Student-private
material cannot be approved or converted to course-wide material.

Files may change only while a material is draft or failed. Finalization freezes
the file set and queues processing once. Ready material is immutable and must be
deleted and recreated to change its source. Approval requires ready state and a
non-empty brief; link-only material does not satisfy course readiness.

The finalization route also retries a failed material after automatic attempts
are exhausted, with or without file changes. MIA has no generic job retry route.

## Tutoring sessions and messages

- `GET|POST /api/v1/courses/{course_id}/tutoring-sessions`
- `GET /api/v1/tutoring-sessions/{id}`
- `POST /api/v1/tutoring-sessions/{id}/completion`
- `POST /api/v1/tutoring-sessions/{id}/summary-generations`
- `GET|POST /api/v1/tutoring-sessions/{id}/messages`
- `POST /api/v1/student-messages/{id}/response-retries`
- `POST /api/v1/tutor-responses/{id}/interruptions`
- `GET /api/v1/tutor-responses/{id}/events`
- `GET /api/v1/tutoring-sessions/{id}/materials`

Session creation optionally includes selected material relationships. The owning
student can complete an active session; no abandon or staff force-completion
route exists.

Each student-message creation includes a client-generated request ID scoped to
the session. Repeating the same ID and content returns the existing message and
response operation. Different content is a conflict.

A session accepts at most one generating response and one queued message. A
further message returns a conflict. Queued work begins after any terminal result
from current generation. Completion returns a conflict while work is queued or
generating. Response retry is available only when the session is idle.

Assigned supervisors can see active-session status but cannot retrieve messages
until completion. Material retrieval performed by the AI tutor uses internal
authorized application tools, not client-selected arbitrary file paths.

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

Each subscriber queue holds at most 64 events or 256 KiB, whichever is reached
first. Overflow closes only that SSE connection; it never blocks provider
consumption or other subscribers. Reconnection recovers through a fresh snapshot.

The reverse proxy must not buffer this route. MIA flushes complete SSE events and
persists generated text in bounded batches rather than writing one database
update for every provider delta.

The interruption route accepts queued and generating responses. A queued
response becomes interrupted without a provider call; a generating response
cancels its provider operation. Startup resumes queued work and marks stranded
generation failed rather than recreating an uncertain provider request.

## Generated speech

- `POST /api/v1/tutor-responses/{id}/speech`
- `GET /api/v1/tutor-responses/{id}/speech`

The POST route requests or reuses generation for the user's selected voice. The
GET route returns JSON:API state while generation is pending or failed and audio
with its documented media type when available. Every request reauthorizes access
to the source response.

When ElevenLabs is not configured, both routes return a stable
feature-unavailable error.

## Mentoring

One resource represents the request, mentor response, optional appointment, and
closure:

- `GET|POST /api/v1/courses/{course_id}/mentoring-sessions`
- `GET|PATCH /api/v1/mentoring-sessions/{id}`

The student may provide `scheduled_for` during creation. Otherwise, the assigned
mentor may add it later. `PATCH` supports the mentor response, rescheduling,
meeting details, and closure subject to field-level authorization and current
state.

MIA provides no live mentoring channel. Meeting URLs are HTTPS metadata and are
never fetched by MIA.

## Jobs and audit

- `GET /api/v1/jobs`
- `GET /api/v1/jobs/{id}`
- `GET /api/v1/materials/{id}/jobs`
- `GET /api/v1/tutoring-sessions/{id}/jobs`
- `GET /api/v1/audit-events`

Administrators can inspect platform jobs. Supervisors receive only the safe
processing status needed for resources in their assigned courses; they do not
receive unrestricted provider diagnostics.

Audit access is administrator-only. Filters and output fields are allowlisted,
and audit resources never expose secret or content-bearing values.

The first version has no manual job retry route. MIA performs only its bounded
automatic retries.

## Open decisions

Exact JSON:API attributes, relationships, includes, filters, and collection
limits remain open and are not implied by the route layout.

Resolve each item in this document or a more specific current architecture
document before implementing the affected routes. Superseded proposals should be
removed rather than retained as history.
