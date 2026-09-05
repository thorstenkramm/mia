---
title: 'ClickSend Base URL Configuration'
type: 'feature'
created: '2026-09-05'
status: 'done'
baseline_commit: 'ba56d278eb99780ca4828d5c5de2051fa77d072f'
review_loop_iteration: 0
context:
  - 'AGENTS.md'
  - 'docs/server-configuration.md'
  - 'docs/architecture.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** MIA's ClickSend adapter has a production endpoint embedded in the
adapter, so an operator cannot direct SMS delivery to a compatible local E2E
test double. The configuration must remain safe: no provider availability check
at startup and no ability to select arbitrary ClickSend endpoint families.

**Approach:** Add an optional `clicksend.base_url` configuration setting with a
safe default and the established TOML, environment, and non-secret flag
precedence. Validate the base URL locally, pass it to the adapter, and compose
only its fixed `POST /sms/send` endpoint.

## Boundaries & Constraints

**Always:** Default to `https://rest.clicksend.com/v3`; allow only a root origin
or `/v3` base path, each with an optional terminal slash; reject userinfo,
queries, fragments, paths other than `/v3`, non-HTTPS schemes, and non-loopback
HTTP. Permit HTTP only for `localhost` or a loopback IP, including a local endpoint such as
`http://127.0.0.1:3550`. Preserve direct adapter callers' default behavior when
they omit the base URL, keep existing ClickSend request/auth behavior, and make
no provider request during configuration loading or startup.

**Ask First:** Any expansion beyond the single ClickSend `POST /sms/send`
endpoint, or an HTTP exception for non-loopback hosts.

**Never:** Modify, revert, stage, or commit the protected pre-existing dirty
changes: the country-validation diagnostic, unrelated user edits, or the
`build.sh` mode change. Do not expose credentials in flags, support arbitrary
provider endpoint paths, or document other ClickSend endpoint families.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Default | No base URL source | Config and direct client use the production base URL | No provider call |
| Override | Flag or environment has local loopback URL | It wins by normal precedence and sends to `/sms/send` | No provider call during load |
| HTTPS base | HTTPS root or `/v3` base with optional trailing slash | Accepted and joined with exactly `/sms/send` | No duplicate slash |
| Local HTTP | `localhost` or loopback IP base | Accepted for compatible E2E test doubles | No provider call during load |
| Unsafe URL | Userinfo, query, fragment, unsupported path, non-loopback HTTP, or other scheme | Configuration is rejected locally | Return a setting-validation error |

</frozen-after-approval>

## Code Map

- `internal/config/config.go` -- `Config.ClickSend`, `settings`, and `validate`
  define the schema, default/override binding, optional-provider presence, and
  startup-local validation. `normalizePublicURL` is a nearby origin-validation
  pattern, but ClickSend requires a separate HTTP-loopback rule.
- `internal/config/config_test.go` -- constructs complete serve configurations,
  tests source-specific overrides, and tests URL normalization helpers. Extend
  these patterns for the new default, flag/environment precedence, and accepted
  and rejected URLs.
- `cmd/mia/main.go` -- creates `sms.ClientOptions` in `newServeCommand`; pass
  only the validated configuration field into this existing construction.
- `internal/provider/sms/sms.go` -- `ClientOptions` and `New` currently accept a
  complete test endpoint and otherwise embed the production send endpoint. Add a
  base URL option and compose the fixed send path while retaining the omitted
  option's default.
- `internal/provider/sms/sms_test.go` -- existing `httptest` assertions cover
  request JSON, Basic authentication, redirects, and provider outcomes. Adapt
  the endpoint setup to assert request method/path and add default, slash, and
  loopback base joining coverage without live provider traffic.
- `mia.example.toml` -- mechanically checked annotated configuration sample;
  add the new documented key within `[clicksend]`.
- `docs/server-configuration.md` -- authoritative operator setting reference;
  add `clicksend.base_url` with its flag, environment variable, default, exact
  purpose, and constrained HTTP-loopback allowance.
- `docs/architecture.md` -- records adapter and provider-operation decisions;
  document the fixed send route/base URL composition without listing other
  ClickSend endpoints.
- `docs/product-requirements.md` -- read-only evidence: ClickSend remains an
  optional SMS provider and receives SMS delivery data; base URL is an operator
  configuration/architecture detail, not a product-behavior change.

## Tasks & Acceptance

**Execution:**
- [x] `internal/config/config.go` -- add `clicksend.base_url`, its default,
  non-secret override flag, optional-provider recognition, and security-focused
  normalization/validation -- permits controlled local E2E routing only.
- [x] `cmd/mia/main.go` -- pass `configuration.ClickSend.BaseURL` to
  `sms.ClientOptions` -- keeps command wiring the sole configuration-to-adapter
  boundary.
- [x] `internal/provider/sms/sms.go` -- accept an optional base URL and derive
  only the ClickSend SMS send URL -- preserves the direct caller default and
  prevents arbitrary endpoint selection.
- [x] `internal/config/config_test.go`, `internal/provider/sms/sms_test.go`, and
  `cmd/mia/main_test.go` -- add focused configuration, validation, wiring, URL
  joining, request-method/path, request-body, and authentication regression
  coverage -- verifies the public configuration and adapter boundaries.
- [x] `mia.example.toml`, `docs/server-configuration.md`, and
  `docs/architecture.md` -- document the setting with the required exact intent,
  default, local-loopback HTTP exception, and no other ClickSend endpoints --
  keeps human-readable contracts aligned.

**Acceptance Criteria:**
- Given no ClickSend base URL override, when MIA loads configuration or a direct
  caller builds a configured SMS client, then the base is
  `https://rest.clicksend.com/v3` and the send URL is
  `https://rest.clicksend.com/v3/sms/send`.
- Given `--clicksend-base-url` or `MIA_CLICKSEND_BASE_URL`, when configuration
  loads, then the corresponding valid value overrides TOML according to the
  established precedence.
- Given valid HTTPS root or `/v3` bases, each with an optional terminal slash,
  `localhost` over HTTP, or a loopback IP over HTTP, when configuration loads, then it
  succeeds without attempting network connectivity.
- Given a base URL containing userinfo, query, fragment, an unsupported path,
  non-loopback HTTP, or an unsupported scheme, when configuration loads, then it
  fails locally.
- Given a local ClickSend-compatible test server base URL, when `Send` runs,
  then it sends exactly one authenticated JSON `POST` to `/sms/send` and keeps
  the existing request and response behavior.

### Review Findings

- [x] [Review][Decision] Allowed ClickSend base path — human decision: accept
  only root origins or `/v3` base paths, each with an optional terminal slash.
- [x] [Review][Patch] Restrict ClickSend URL validation to serving configuration
  [internal/config/config.go:204]
- [x] [Review][Patch] Align bootstrap terminal-mode documentation with approved
  complete flag-file invocation [docs/api.md:140]
- [x] [Review][Patch] Validate ClickSend URL ports before provider delivery
  [internal/config/config.go:343]
- [x] [Review][Patch] Test invalid base URLs through TOML, environment, and flag
  configuration loading [internal/config/config_test.go:209]
- [x] [Review][Patch] Test `/v3` base URL composition to exactly `/v3/sms/send`
  [internal/provider/sms/sms_test.go:98]
- [x] [Review][Patch] Add invitation acceptance coverage for invalid country
  mapping to the validation response [internal/invitation/handler.go:298]

## Spec Change Log

## Design Notes

The configuration owns URL validation because operator input must be rejected
before wiring. The SMS adapter owns fixed route composition, because it is the
only component that knows MIA supports one ClickSend operation. This preserves a
small, explicit boundary: a base URL is configurable, but adapter behavior is
not a general ClickSend client.

## Verification

**Commands:**
- `gofmt -w internal/config/config.go internal/config/config_test.go cmd/mia/main.go cmd/mia/main_test.go internal/provider/sms/sms.go internal/provider/sms/sms_test.go` -- expected: changed Go files format cleanly.
- `go test ./internal/config ./internal/provider/sms ./cmd/mia` -- expected: focused configuration, adapter, and command tests pass.
- `go test ./...` -- expected: all repository tests pass.
- `go vet ./...` -- expected: no vet findings.
- `golangci-lint run ./...` -- expected: no lint findings.
- `go test -race ./...` -- expected: race-enabled suite passes.
- `./run-all-tests.sh` -- expected: repository aggregate checks pass.
- `npx @taplo/cli fmt mia.example.toml && npx @taplo/cli check mia.example.toml && npx @taplo/cli fmt --check mia.example.toml` -- expected: TOML is valid and formatted.
- `npx markdownlint --fix docs/server-configuration.md docs/architecture.md` -- expected: changed Markdown is compliant.
- `git diff --check` -- expected: no whitespace errors.
