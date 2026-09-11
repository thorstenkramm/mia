# Code Duplication

`run-all-tests.sh` runs JSCPD over Go and markup sources at `--threshold 0`, so any reported clone fails the project
check. This document governs how to react to a report. It applies to every change, not to one remediation task.

## The gate

Never raise `--threshold`, `--min-lines`, or `--min-tokens`, and never add a whole-file ignore outside generated or
third-party code. Tuning those values changes what the project considers acceptable duplication and needs an explicit
human decision, not a quiet edit to make a report pass.

Run the check before finishing work that adds or moves code. A gate nobody invokes does not hold: stories 1-10 through
1-13 each left the check failing because it was never run, and the ten accumulated clones then had to be cleared in one
separate task.

## Deciding between a refactor and a marker

Assess every reported clone for one concrete shared responsibility before touching it.

Refactor when the duplicated sequence really is one responsibility. Look for an existing owner first, because the
shared behavior often already exists and is simply unused at one site. Three of the clones cleared in the first two
passes were exactly that: `scanResponse`, `deleteSessions`, and a collection handler already existed nearby. That is a
half-converted pattern, not separate ownership.

Place genuinely shared behavior with the layer that owns it. Protocol and JSON:API concerns belong in
`internal/httpserver`, persistence helpers in `internal/sqlite`, and job lifecycle rules in `internal/jobs`. Never
create a `util` or `misc` package to hold it, and do not invent an abstraction that only a duplication report asks for.

Mark the clone instead when the two sites own different authorization, security, lifecycle, provider, or domain
contracts, or when an interface fixes both shapes and neither can differ. Unifying such code moves decisions away from
the package responsible for them, which is worse than the duplication.

## Writing a marker

Use only `jscpd:ignore-start` and `jscpd:ignore-end`, covering the smallest range that suppresses the report. The
markers are plain text matches, so both line and block comments work.

Marking one side suppresses the whole pair, so add one marker range and state the reason in a comment immediately
adjacent to it. Put a short note at the other site too, naming where the marker lives, so a reader there is not left
wondering why matching code is unmarked.

> [!IMPORTANT]
> A `jscpd:ignore-start` without its matching `jscpd:ignore-end` silently suppresses detection to the end of that file
> and still exits successfully. A dropped marker is indistinguishable from a clean result, so `run-all-tests.sh` checks
> marker balance before running the scan. Never remove that check.

Marking also shrinks the reported denominator, because a file whose entire tokenizable content is ignored drops out of
the analyzed set. Treat the reported percentage as an indicator, not a target.

## When duplicated sites disagree

Duplication can hide a latent defect. If the copies differ in error handling, transaction scope, or any other
behavior, that divergence is a finding that needs a decision. It is not something to preserve, and the majority spelling
does not win by count.

Decide on correctness, apply one behavior, and record the change and its reason where reviewers will see it. Never
encode the weaker behavior into a shared helper merely to keep a refactor behavior-neutral, and never split a helper in
two to keep an unexplained inconsistency alive. Pin the resulting behavior with a test, since duplication that nothing
tested is how the inconsistency survived.

Stop and ask instead of deciding alone when the divergence touches an authorization, security, or data-lifecycle
contract, or when resolving it needs a product decision.

## After refactoring

Re-run the check. A refactor can trade one clone for a larger one: replacing per-package list handlers with a shared
helper once left two call sites identical at 146 tokens, more than the 76-token clone it removed. Measuring caught it,
and making the helper carry the repeated mapping removed the duplication instead of hiding it behind a marker.

Never reformat, rename, or reorder code to evade detection. That defeats the check while leaving the duplication in
place. Equally, do not force duplication to zero by merging unrelated code; AGENTS.md's guidance stands, and removing
duplication is worthwhile only when one abstraction has a clear responsibility.
