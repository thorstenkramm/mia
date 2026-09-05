# JSON:API Contract

This rule is the normative contract for HTTP representation and protocol behavior of the MIA API. It uses `MUST`,
`MUST NOT`, `SHOULD`, and `MAY` as defined in RFC 2119. Non-normative examples live in
`.agents/references/json-api-examples.md`.

## Scope and Precedence

- The adopted specification is JSON:API 1.1 (`application/vnd.api+json`). See
  <https://jsonapi.org/format/#fetching> and <https://jsonapi.org/format/#crud>.
- Product requirements govern behavior, authorization, and data lifecycle. This rule governs HTTP representation and
  protocol behavior only.
- When this rule and the upstream JSON:API specification differ, this rule wins.
- Exceptions MUST be named in this rule. A feature package MUST NOT implement a local exception.

## Protocol Ownership

- Shared JSON:API protocol behavior MUST live in `internal/httpserver`, not in feature packages.
- Feature packages MUST NOT add package-local implementations of media negotiation, bounded JSON:API decoding, error
  envelopes, pagination parsing, navigation-link construction, or instant formatting.
- Feature packages own request attributes, resource construction, authorization, and domain errors.
- Routes MUST be registered with representation and authentication classifications
  (public, authentication-sensitive, or authenticated).

## Route Representations

| Route representation | Request content type        | Response content type      | JSON:API negotiation |
|----------------------|-----------------------------|----------------------------|----------------------|
| JSON document        | `application/vnd.api+json`  | `application/vnd.api+json` | Required             |
| Multipart upload     | `multipart/form-data`       | JSON:API or `204`          | Response only        |
| Raw image upload     | `image/jpeg` or `image/png` | No body                    | No                   |
| Binary or PNG        | None                        | Allowlisted media type     | No                   |
| NDJSON               | None                        | `application/x-ndjson`     | No                   |
| SSE                  | None                        | `text/event-stream`        | No                   |
| Bodyless success     | None or route-specific      | No body                    | No response document |

- JSON document requests MUST use exactly `application/vnd.api+json` without media-type parameters. `charset` is
  forbidden. A violation returns `415`.
- JSON:API response bodies MUST set `Content-Type: application/vnd.api+json`.
- `Accept` negotiation is spec-minimal: when the `Accept` header contains instances of `application/vnd.api+json`
  and every one of them carries media-type parameters, the server MUST return `406`. A missing `Accept`, `*/*`, and
  any request containing at least one parameterless `application/vnd.api+json` instance are acceptable. Full generic
  content negotiation is deliberately out of scope.
- Raw image, binary, PNG, multipart, SSE, and NDJSON routes are exempt from JSON:API negotiation.
- Unknown routes and unsupported methods MUST reach ordinary `404` or `405` routing before `Accept` negotiation.

## Documents and Resources

- A successful JSON response MUST have a top-level `data` member. An error response MUST have a top-level `errors`
  array. A response MUST NOT contain both.
- Every response resource object MUST contain `type`, `id`, and its remaining fields nested under `attributes` or
  `relationships`.
- Resource types MUST be plural nouns in kebab-case, such as `materials` or `mfa-management-proofs`.
- Attribute names MUST use snake_case, such as `first_name` and `created_at`.
- Empty collections MUST serialize as `data: []`, never `null`.
- `meta` MAY carry extra information such as `has_more`.

## Resource Identity

- Every response resource MUST have a unique, opaque, non-secret `id` within its `type`.
- Every PATCH request document MUST include `type` and a non-empty `id` exactly matching the path resource ID.
  A missing or mismatched identity returns the endpoint's documented `422` validation error.
- Create requests MUST NOT accept client-generated IDs. The server assigns every ID.
- For the singleton alias `PATCH /api/v1/users/me`, the request ID MUST equal the authenticated account ID, not the
  literal string `me`.
- Command and ephemeral resources (login attempts, password changes, challenges, proofs) MUST receive unique opaque
  IDs unless this rule explicitly defines a bodyless response for that operation or names an exception below.
- Secret tokens, password values, MFA values, and their digests MUST NOT be used as resource IDs.

### Named Exception: auth-sessions Identity

`auth-sessions` is a per-account singleton view of the current cookie session, not an event log. Its resource ID is
the account ID: repeated logins, MFA verifications, and password-change stages for one account intentionally return
the same `(type, id)` because they describe the same conceptual resource — the current authentication state of that
account. MIA keeps no server-side browser-session records, so login attempts and session stages are not addressable
resources and MUST NOT receive fabricated unique IDs that name nonexistent state.

## Links

- Links are OPTIONAL everywhere except paginated collections.
- Every paginated collection response MUST include `links.next` and `links.prev` when those pages exist. Absent
  navigation links MUST be omitted rather than set to `null` or an empty string.
- A command resource without a GET route MUST NOT invent a resource self link.
- Navigation links MUST be built by the shared link builder in `internal/httpserver`, which preserves only
  allowlisted pagination parameters.

## Pagination

- Every collection that can grow with user or operational data MUST use offset pagination.
- A collection may remain unpaginated only when a documented database or product invariant enforces a small hard
  cap; the exemption MUST name the invariant.
- Parameters are exactly `page[limit]` and `page[offset]`.
- Defaults are limit 25 and offset 0. The maximum limit is 100 and the maximum offset is 10,000.
- Duplicate, unknown, empty, non-integer, negative, and out-of-range pagination parameters MUST be rejected.
- Ordering MUST be deterministic and include a unique tie-breaker column.
- Parameter validation is tested once in the shared parser's test suite. Endpoints need only thin wiring tests
  proving they use the shared parser.

## Trailing Slashes

- Canonical API routes have no trailing slash. Collections use plural nouns (`/files`); elements append the
  identifier (`/files/foo`).
- A trailing-slash variant MUST return the ordinary JSON:API `404` response.
- The server MUST NOT redirect or serve identical content on both forms.

## Date and Time

- Every API field representing an instant MUST use RFC 3339 in UTC with the `Z` suffix in requests and responses.
- API responses MUST normalize instants to exactly six fractional digits. Persistence MUST remain UTC but MAY use a
  feature-local layout when that layout expresses the SQLite storage contract.
- Requests MAY carry up to nine fractional digits. Instant values with a non-zero numeric offset MUST be rejected.
- Absent optional instants MUST serialize as JSON `null`.
- All API instant output MUST go through the shared formatter and all API instant input through the shared strict
  parser in `internal/httpserver`. Feature-local API timestamp layouts are forbidden; persistence parsing and
  serialization are outside this prohibition.
- Field names use `_at` for event instants, `_from` and `_until` for range boundaries, and `_for` for a scheduled
  target instant.
- Dates, durations, local times, and recurring schedules are not instants. A local-time schedule uses separate
  local-time and IANA time-zone fields, such as `16:00:00` and `Europe/Berlin`.
- Clients display instants in the viewer's preferred IANA time zone; the server uses that preference for generated
  communications.

## Rate Limiting

- An unauthenticated endpoint is a publicly accessible route, not any request that happens to lack a session.
- The global unauthenticated API limit applies only to public routes. Authenticated routes MUST NOT consume it.
- Authentication, MFA, invitation, registration, password-reset, and SMS-code endpoints require dedicated layered
  limits in addition to the global public limit.
- The MVP has no separate authenticated tutoring, upload, material-finalization, or speech rate limits.
- A rate-limit denial MUST return HTTP `429` with the central JSON:API error and a stable code, and MUST include
  `Retry-After` when the next permitted retry time is known.
- Rate-limit responses MUST NOT reveal whether an account, invitation, mobile number, or other sensitive identifier
  exists.

### Named Exception: Password Recovery

Password-recovery throttling that must remain indistinguishable from an accepted recovery request returns an empty
`204`. It is exempt from the general JSON:API `429` response requirement. The suppression is audited without
username, account-existence, or password data.

New exceptions MUST be approved as product behavior and added to this rule before implementation.

## Protocol Error Classification

| Condition                                          | Status |
|----------------------------------------------------|--------|
| Malformed JSON:API structure or malformed Unicode  | `400`  |
| No acceptable response representation              | `406`  |
| Request body exceeds its bound                     | `413`  |
| Unsupported request media type                     | `415`  |
| Valid document with invalid resource data          | `422`  |
| Rate limited, except the named recovery exception  | `429`  |
| Unknown API route                                  | `404`  |
| Unsupported API method                             | `405`  |

- Every entry MUST use the central JSON:API error registry in `internal/httpserver`.
- Duplicate object-member names at any depth and unknown or unexpected document or resource members are malformed
  JSON:API structure and MUST return `400 malformed_request`. A known member with an invalid resource value is a
  validly structured document with invalid resource data and MUST return the endpoint's documented `422` code.
- The shared protocol layer classifies the failure first; feature packages select a domain-specific stable code only
  for the `422` class and domain errors.
- Error objects MUST contain string `status`, stable `code`, `title`, and `detail`. Details MUST NOT leak resource
  existence or internal state.

## API Documentation

- `api-doc/openapi.yaml` is the source of truth for concrete HTTP transport details and MUST follow OpenAPI 3.2.
- `docs/api.md` carries product rationale and cross-route narrative only; field-by-field contracts MUST NOT be
  duplicated there.
- The documentation MUST be split into multiple files with this layout:

  ```text
  api-doc/
    openapi.yaml
    paths/
    schemas/
      json-api/
      resources/
    responses/
  ```

- Every endpoint change MUST update its path, request, response, security, stable-error, and bound definitions in
  the same commit.
- Validation uses the pinned Redocly CLI version:

  ```shell
  npx @redocly/cli@2.49.1 lint --config .redocly.yaml ./api-doc/openapi.yaml
  ```

### Documentation Standards

- Every operation's success response MUST reference a concrete response schema. Description-only success responses
  are forbidden except for bodyless `204` responses.
- Every JSON:API request body MUST reference a per-resource request schema that declares its `data.type`, whether
  `data.id` is required, and every writable attribute with its type, required flag, and bounds. The generic
  free-form document schema is reserved for the shared envelope definitions and MUST NOT be an operation's request
  contract.
- Resource schemas under `schemas/resources/` MUST be referenced by every operation that returns them. Unreferenced
  schemas are dead documentation and MUST be removed or wired.
- Every operation MUST declare its protocol error responses (`400`, `406`, `413`, `415` where a body is decoded,
  `429` where a limiter applies) and its domain error responses (`401`, `403`, `404`, `409`, `422`) with their
  stable codes, reusing the shared responses in `responses/`.
- Every operation MUST declare its security requirements: the session cookie scheme for authenticated routes and
  the CSRF header scheme for every unsafe method that the CSRF middleware enforces.
- Existence-hiding endpoints MUST document the uniform response, never per-cause responses that reveal resource
  existence.
- `.redocly.yaml` MUST keep the `recommended` ruleset with `operation-4xx-response` and `no-unused-components`
  enabled. Disabling a lint rule requires a named exception in this rule file with its justification.
- A passing lint is necessary but not sufficient. Endpoint review MUST verify schema coverage: request and response
  bodies resolve to concrete typed schemas, not free-form objects.

## Conformance Suite

The shared black-box kernel tests in `internal/httpserver` MUST exhaustively verify shared protocol behavior:

- media negotiation and response content type;
- success and error top-level members;
- resource `type`, `id`, attributes, and relationships;
- PATCH identity;
- body bounds and protocol error classification;
- instant formatting;
- pagination and navigation links;
- strict trailing-slash rejection;
- rate-limit format and the named recovery exception.

Every implemented route family MUST add representative real-route wiring tests for the shared behaviors relevant to
that family, including non-JSON representation exemptions and layered limiting where applicable. An endpoint need not
repeat the complete kernel matrix. Feature tests add domain behavior without mechanically duplicating shared protocol
assertions.

## Audit Boundary

Protocol failures classified before domain handling, including `400`, `413`, and `415`, are not login or domain
attempts and MUST NOT create domain-denial audit events. A validly structured credential or domain attempt that fails
validation, authentication, authorization, or domain rules retains its documented audit behavior. Protocol handling
MUST NOT inspect or audit request secrets merely to create an attempt record.

## Endpoint Change Checklist

Complete this checklist whenever an endpoint is added or changed:

1. Register the route with representation and authentication classifications.
2. Use shared JSON:API decode, response, error, pagination, link, and instant helpers.
3. Add authorization and existence-hiding tests.
4. Run shared protocol conformance tests.
5. Update the split OpenAPI operation and reusable schemas.
6. Run Go verification and the pinned API lint command.

Worker and reviewer reports MUST list the API lint result.
