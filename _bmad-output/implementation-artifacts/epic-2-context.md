# Epic 2 Context: Reliable Browser-Ready Workflows

<!-- Compiled from planning artifacts. Edit freely. Regenerate with compile-epic-context if planning docs change. -->

## Goal

Deliver authoritative, privacy-safe browser contracts for discovering current state, understanding available actions,
and reconciling ambiguous or concurrent workflow outcomes without reconstructing backend policy in the frontend.

## Stories

- Story 2.1: Protect Browser Responses and Bound Error Disclosure
- Story 2.2: Discover and Extend Browser Sessions Safely
- Story 2.3: Manage Invitations Through Delivery Ambiguity
- Story 2.4: Discover Account Capabilities and Administration Targets
- Story 2.5: Inspect MFA State and Recover Lost Factors
- Story 2.6: Observe and Continue Mobile Verification
- Story 2.7: Understand Course Readiness and Confirm Destructive Course Actions
- Story 2.8: Discover and Resume the Active Tutoring Session
- Story 2.9: Observe and Control Current Tutoring Work
- Story 2.10: Follow Tutoring Summary Generation and Regeneration
- Story 2.11: Request Mentoring and Complete Eligible Work
- Story 2.12: Resolve and Grant Mentor Targets Safely

## Requirements & Constraints

Browser contracts must remain existence-safe across missing, out-of-scope, stale, and changed resources. Responses must
expose only authorized current state, server-authored action eligibility, and bounded metadata. Sensitive API output and
errors must not be stored by browsers or intermediaries. Error bodies must be complete, bounded JSON:API documents that
never expose secrets, personal data, internals, prompts, message bodies, or provider payloads. Authentication, CSRF,
rate-limit, and resource-state transitions remain authoritative on every request. Mutations that depend on a previously
reviewed target or state must bind that intent atomically and return safe conflicts when it has changed.

## Technical Decisions

The shared HTTP kernel owns response policy, JSON:API encoding, error mapping, media negotiation, trusted client IPs,
rate limiting, sessions, and CSRF. One registry supplies stable safe error codes and one mapping layer converts errors;
feature handlers never construct error envelopes. Resource access uses authorization-scoped fetches that make unknown
and out-of-scope resources indistinguishable. Browser sessions remain signed, encrypted, and stateless while account,
role, assignment, ban, and password-gate state is reloaded for every protected request. Cross-feature mutations use the
owning APIs in one transaction, and asynchronous results retain guarded-commit behavior.

## UX & Interaction Patterns

Discovery responses distinguish explicit empty state from unavailable or expired access and provide enough opaque
identity for authorized follow-up navigation. Eligibility is server-authored and separate from lifecycle state. Reads
used for reconciliation do not silently mutate workflow state. Ambiguous external delivery or mutation outcomes expose
sanitized state and timing rather than claiming success or retrying automatically.

## Cross-Story Dependencies

Story 2.1 establishes response and error protections used by every later browser contract. Story 2.2 defines session and
CSRF lifecycle behavior used by all authenticated discovery and mutation stories. Capability and target discovery
stories supply authoritative identities and eligibility consumed by later intent-bound mutations.
