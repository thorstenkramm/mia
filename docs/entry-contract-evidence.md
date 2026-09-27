# Entry contract evidence

The [OpenAPI description](../api-doc/openapi.yaml) owns the transport contract. This page maps its entry guarantees
to executable backend evidence; it is not a second schema or a browser-acceptance report.

## Contract references

- [Authentication operations](../api-doc/paths/auth.yaml): `sessionDiscovery` and the shared `Session` response,
  including the four named discovery examples. Shared authentication schemas are tested separately; the shared
  response has no examples that could advertise an impossible outcome for a particular operation.
- [User operations](../api-doc/paths/users.yaml): `capabilities`, with complete `singleRole`, `multiRole`, and
  `emptyAssignments` examples. Example identities are synthetic and carry no identifier-format requirement.
- [Resource schemas](../api-doc/schemas/resources/resources.yaml): `AuthSession`, `AuthSessionDocument`,
  `SessionDiscoveryDocument`, and `AccountCapabilitiesDocument`.
- [API narrative](api.md#browser-session-and-csrf) explains session and CSRF lifecycle behavior.

## Schema and example evidence

Tests in [api-doc/entry_contract_test.go](../api-doc/entry_contract_test.go):

- `TestEntryStageSchemaConstraints` validates shared authentication and discovery documents. MFA requires a non-empty
  opaque identity; missing, empty, null, numeric, boolean, array, and object identities fail. Non-MFA authentication
  stages reject a challenge member. Anonymous discovery remains valid; unrelated attribute extensions remain allowed.
  An invalid deadline verifies that format assertions are active.
- `TestEntryResponseExamples` compiles the actual operation response schemas and validates every named entry example
  directly from OpenAPI YAML. Capability examples contain every action
  exactly once, with complete scope fields.
- `TestEntrySchemaLoaderRejectsNonlocalReferences` verifies local-only loading. The pinned Draft 2020-12 validator
  rejects network and out-of-tree references with explicit policy errors, including valid outside-root schemas and
  symlink escapes in temporary fixtures. Compilation must succeed before instance rejection can count as evidence.

## Authentication and CSRF evidence

Tests in [internal/auth/auth_test.go](../internal/auth/auth_test.go):

- `TestSessionDiscoveryCSRFMatrix` exercises all four stages with missing, existing, and duplicate configured-name CSRF
  cookies, under HTTPS and loopback cookie policies. GETs have no CSRF header or body. It checks issuance versus
  retention, anonymous rotation, JSON:API duplicate denial without data, no-store, no authentication or browser-marker
  reissuance for valid stages, and body/header deadlines matching the original staged deadlines.
  The CSRF helper selects the last-issued value when anonymous discovery issues twice.
- `TestDiscoveryRotationRejectsMismatchWithoutRevokingPriorPair` exercises a real unsafe recovery route: an old header
  with the replacement cookie fails; both the new matching pair and a prior matching pair pass CSRF validation.
- `TestSessionDiscoveryReturnsAnonymousAndAuthoritativeStages`, `TestMFADiscoveryReturnsOnlyOpaqueChallenge`, and
  `TestBrowserRestartRetainsBoundAuthenticatedSession` cover stage serialization, challenge binding, stale password
  stages, and authenticated discovery.
- `TestSessionDiscoveryInvalidAuthenticationIsUniform` covers missing, malformed, expired, banned, deleted,
  security-generation-invalid, and malformed-browser-marker authentication. The malformed session case retains a
  valid browser marker to reach authentication-cookie decoding rather than the missing-marker guard.
- `TestMalformedBrowserMarkerRecoveryRemainsAnonymousUntilLogin` checks the approved recovery repair under both cookie
  policies: strict authentication denial, anonymous recovery with a fresh marker, rejection of the old session paired
  with that marker, valid marker reuse, and usable discovery after fresh login.
- `TestSessionDiscoveryDependencyFailureRemainsAnError` ensures an identity dependency failure is a no-store JSON:API
  error rather than anonymous success, with no partial data or internal detail.
- `TestMFAVerificationPrecedesPasswordChange` and `TestMFACompletionSessionsWorkOnNextRequest` cover MFA ordering and
  usable transitions. `TestOnlyContinueWorkingExtendsAuthenticatedSession` covers non-refreshing ordinary requests.
- `TestLogoutReorderingUsesReducedStatelessGuarantee` protects the accepted logout limitation: delayed ordinary
  responses cannot restore a matching pair, but an admitted authentication transition can; other browsers remain valid.

Tests in [internal/httpserver/httpserver_test.go](../internal/httpserver/httpserver_test.go):

- `TestBrowserMarkerSaveFailureIsReturned` forces a real CookieStore encoding failure and verifies it remains a save
  error rather than being swallowed by malformed-marker recovery.
- `TestCookiePolicyAttributesAndLocalHTTPCookieJarCSRF` covers cookie policy and unsafe-method CSRF enforcement.

## Capability evidence

Tests in [internal/capability/capability_test.go](../internal/capability/capability_test.go):

- `TestCapabilitiesDiscoveryCSRFMatrix` covers headerless/bodyless discovery with absent, existing, and duplicate CSRF
  cookies, including anonymous and restricted-stage denials without data.
- `TestCapabilitiesRejectRestrictedStages` checks MFA, password replacement, and MFA-before-password-replacement.
- `TestRegisteredCapabilitiesRouteRequiresCurrentAuthentication` checks absent/expired authentication and a successful
  read without session refresh.
- `TestUnassignedAccountsDiscoverExplicitEmptyScopes` checks success for every role with empty assignment arrays.
- `TestCurrentReturnsExplicitUnionedScopes` checks multi-role scoped union rather than role-based scope expansion.
- `TestMentorScopePreservesPairsAndReflectsRemoval` checks exact course/student pairs, exclusion of another mentor's
  assignments, and current scope after removal.
- `TestCapabilitiesFailureReturnsNoPartialScope` checks dependency failure after populated scope, without leaking
  partial assignments. The shared `readCapabilities` assertion verifies all 14 expected action names exactly once.

## Running and interpreting the evidence

Run `./run-all-tests.sh` from the backend repository root. It includes the schema tests through `go test ./...`.
Provider interactions in these tests are local fakes or provider-free paths; no paid or production provider is called.

Backend evidence does not establish frontend browser acceptance. The frontend still owns ordered entry routing,
protected-content suppression, error-state presentation, and its Story 1-2 scope/dependency reconciliation. This work
does not close all of BCG-001 or BCG-003. Broader lifecycle work, bounded capability pagination, SSE, tutoring
continuation, and stronger logout revocation remain outside this correction.
