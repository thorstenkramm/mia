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

### Identifiers and time

- Resource IDs are opaque and clients never derive authorization from them.
- Every instant uses RFC 3339 UTC with the `Z` suffix.
- Clients convert display-local values to UTC before sending an instant.
- One-time mentoring appointments use `scheduled_for` as a UTC instant.

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
reset link expires 30 minutes after issuance and is single-use.

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

## Invitations

Authenticated invitation management uses stable invitation IDs:

- `GET|POST /api/v1/invitations`
- `GET|DELETE /api/v1/invitations/{id}`
- `POST /api/v1/invitations/{id}/resends`

Public acceptance submits the invitation token in the request body so tokens do
not appear in access-log paths:

- `POST /api/v1/invitation-acceptances`

Supervisor and mentor invitations are single-use, do not expire, and remain
pending until accepted or revoked. Student accounts do not use invitations.

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
- `POST /api/v1/users/{id}/bans`
- `DELETE /api/v1/users/{id}/bans`

These routes do not create a generic administrator override. Each operation
enforces its role, course, student, and protected-field rules. Student
provisioning and course membership use the course routes below.

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
needed for supervisor provisioning. It is not a public registration workflow.

Mentor removal accepts the required replacement mentor when open mentoring
sessions must be reassigned. Course deletion remains restricted to an inactive
course with no active tutoring sessions.

## Materials and files

- `GET|POST /api/v1/courses/{course_id}/materials`
- `GET|PATCH|DELETE /api/v1/materials/{id}`
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

## Tutoring sessions and messages

- `GET|POST /api/v1/courses/{course_id}/tutoring-sessions`
- `GET /api/v1/tutoring-sessions/{id}`
- `POST /api/v1/tutoring-sessions/{id}/completion`
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

Assigned supervisors can see active-session status but cannot retrieve messages
until completion. Material retrieval performed by the AI tutor uses internal
authorized application tools, not client-selected arbitrary file paths.

### Tutor-response events

`GET /api/v1/tutor-responses/{id}/events` returns a Server-Sent Events stream
with media type `text/event-stream`. Establishing a stream reauthorizes access to
the response. The stream exposes MIA events only; raw OpenAI events, tool calls,
provider errors, and provider payloads are never forwarded.

On every connection, MIA first sends the current persisted content and state:

```text
event: snapshot
data: {"content":"Current persisted text","state":"generating"}
```

New text and terminal state changes use these events:

```text
event: delta
data: {"text":"next text"}

event: completed
data: {"state":"completed"}

event: interrupted
data: {"state":"interrupted"}

event: failed
data: {"state":"failed","code":"provider_failure"}
```

MIA may send SSE comment heartbeats to keep an otherwise idle connection open.
A browser disconnect closes only that subscription; it does not cancel response
generation. Reconnecting to the same route receives a fresh snapshot followed by
new deltas, so MIA does not need a persisted per-token event history.

The reverse proxy must not buffer this route. MIA flushes complete SSE events and
persists generated text in bounded batches rather than writing one database
update for every provider delta.

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
