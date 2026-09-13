---
title: 'Protect Browser Responses and Bound Error Disclosure'
type: 'feature'
created: '2026-09-13'
status: 'done'
review_loop_iteration: 0
baseline_commit: '5af55587f2973f1ec36b2c770ab01724aa660416'
context:
  - AGENTS.md
  - .agents/rules/echo.md
  - .agents/rules/json-api.md
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** API responses currently use inconsistent cache policy, including authenticated image and speech caching,
and the central error writer does not explicitly enforce the browser-visible 64 KiB complete-document ceiling.

**Approach:** Enforce `Cache-Control: no-store` once at the API boundary for every representation and response status,
remove image caching exceptions, and serialize errors only from bounded safe registry information with a generic fallback.

## Boundaries & Constraints

**Always:** Cover public and authenticated API JSON, errors, SSE, binary, NDJSON, and bodyless responses. Emit error bodies
as valid UTF-8 JSON:API using `application/vnd.api+json`, no larger than 65,536 bytes. Preserve status, stable code, and
existence-hiding semantics when generic fallback is needed. Document that reverse proxies preserve or strengthen
`no-store` and never buffer or cache protected responses.

**Ask First:** Any proposal to expose dynamic dependency error text or create a caching exception for an API response.

**Never:** Apply API no-store policy to separately served frontend assets, silently truncate JSON, leak raw errors,
provider payloads, internal paths, secrets, credentials, cookies, personal data, prompts, or message bodies, or add
feature-local error envelopes.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| API representations | JSON, SSE, binary, NDJSON, or 204 | Every response has `Cache-Control: no-store` | Applies to errors too |
| Authenticated image | Avatar or course logo with validator request | Full authorized PNG with no-store; no caching exception | Hidden/absent stays safe |
| Registry error | Safe registered definition | Complete JSON:API document at mapped status | No raw cause disclosed |
| Oversized registry detail | Encoded error would exceed 65,536 bytes | Same status/code with generic safe title/detail | Never truncate bytes |
| Framework/dependency error | Unsafe or arbitrary error text | Internal generic registry representation | Raw content only in safe logs |

</frozen-after-approval>

## Code Map

- `internal/httpserver/httpserver.go` -- middleware order and current image cache helper; central API policy belongs here.
- `internal/httpserver/errors.go` -- sole registry and HTTP error mapping layer; encode bounded documents here.
- `internal/httpserver/errors_test.go` -- add registry mapping, fallback, UTF-8, media-type, and byte-ceiling tests.
- `internal/httpserver/httpserver_test.go` -- exercise no-store across success, error, bodyless, binary, and streaming forms.
- `internal/{course,speech,material,tutoring}/*.go` -- remove response-local cache policies that can override the kernel.
- `internal/{course,user}/*_test.go` -- update authenticated image expectations and reject validator-based 304 behavior.
- `api-doc/openapi.yaml`, `api-doc/responses/errors.yaml`, `api-doc/paths/{courses,users,mentoring,speech}.yaml` --
  document global no-store, bounded errors, and removal of authenticated-image caching exceptions.
- `docs/api.md`, `docs/server-configuration.md` -- update browser and reverse-proxy response policy narrative.

## Tasks & Acceptance

**Execution:**
- [x] `internal/httpserver` and affected feature handlers -- enforce one non-overridable no-store policy and bounded errors.
- [x] `internal/httpserver/*_test.go` and affected feature tests -- cover all protected representations and error classes.
- [x] `api-doc/` and `docs/` -- align concrete and operator-facing contracts with the implemented policy.

**Acceptance Criteria:**
- Given any public or authenticated API response, when sent, then it includes `Cache-Control: no-store` without image
  exceptions.
- Given any API error, when decoded completely, then it is valid UTF-8 JSON:API, uses the documented media type, is at
  most 65,536 bytes, and contains only safe registry information.
- Given an oversized registry representation or arbitrary dependency failure, when mapped, then a complete generic safe
  error preserves the correct status and stable code without forwarding or truncating unsafe detail.
- Given documented proxy deployment, when API or SSE output passes through it, then storage, replay, and buffering are
  prohibited or the application policy is strengthened.

## Spec Change Log

## Design Notes

Set API cache policy at response-write time or remove every later writer that could override it. Marshal the complete
error document before writing; if it exceeds the ceiling, rebuild from fixed generic title/detail while retaining the
mapped status and code.

## Verification

**Commands:**
- `gofmt` on changed Go files -- expected: no formatting diff.
- `./run-all-tests.sh` -- expected: all Go, race, duplication, vulnerability, OpenAPI, and Markdown checks pass.

## Suggested Review Order

**Response enforcement**

- Final-write middleware prevents any API handler from weakening no-store.
  [`httpserver.go:440`](../../internal/httpserver/httpserver.go#L440)

- Removing validators closes the previous authenticated-image caching exception.
  [`handler.go:518`](../../internal/course/handler.go#L518)

**Bounded disclosure**

- Complete-document sizing replaces oversized registry text without truncating JSON.
  [`errors.go:296`](../../internal/httpserver/errors.go#L296)

- OpenAPI defines the shared no-store header and bounded error contract.
  [`openapi.yaml:180`](../../api-doc/openapi.yaml#L180)

**Verification and operations**

- Representation and error matrices cover no-store, UTF-8, bounds, and disclosure.
  [`response_policy_test.go:15`](../../internal/httpserver/response_policy_test.go#L15)

- Reverse-proxy requirements prevent storage, replay, buffering, and unsafe proxy errors.
  [`server-configuration.md:165`](../../docs/server-configuration.md#L165)
