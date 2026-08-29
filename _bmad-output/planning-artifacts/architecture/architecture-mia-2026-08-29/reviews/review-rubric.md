# Rubric Review — Architecture Spine (MIA backend MVP)

Reviewed: `ARCHITECTURE-SPINE.md` (268 lines, 2026-08-29 draft) against the good-spine checklist.
Verification basis: PRD `prd-mia-2026-08-29` (FR-1..84, NFR-1..21), repo docs (`docs/architecture.md`,
`docs/server-configuration.md`, `docs/database-layout.md`, `docs/data-dir.md`), and AGENTS.md.

## Per-item verdicts

### 1. Fixes the real divergence points for feature slices — **adequate**

The twelve ADs hit the divergence points that matter most for independently built vertical slices:
package shape (AD-1/AD-2), table ownership and cross-feature transactions (AD-3), authorization-scoped
fetch with existence-hiding (AD-4), the three-execution-system split (AD-5), guarded commits (AD-6),
one error registry/mapping layer (AD-7), session handling (AD-8), DB access (AD-9), provider adapters
(AD-10), file publication (AD-11), and same-tx audit (AD-12). The Consistency Conventions table closes
the classic per-slice drift traps (IDs, instants, API shape, uniqueness keys, text normalization,
credentials, redaction).

Two divergence points are missed — see F1 (logging) and F2 (file-write machinery ownership).

### 2. Every AD's Rule is enforceable and prevents its stated divergence — **strong**

Each AD states a checkable rule, not an aspiration. AD-2 (acyclic feature imports, kernel never imports
features) is mechanically verifiable. AD-3 (one owner per table, `*sql.Tx` passed into owners) is
reviewable per migration and per query. AD-4's "missing and out-of-scope return the identical not-found
result" and "handlers never fetch first and authorize second" are concrete review criteria. AD-5 pins
numbers (2-minute lease, 30-second renewal, ≤3 attempts, 16 KiB/1 s batching) rather than adjectives.
AD-6's zero-row-means-stop rule is directly testable. AD-7 forces a single mapping layer in
`httpserver`, which is structurally enforceable. No AD relies on undefined terms; the one subjective
boundary ("bounded feature", AD-1) is pinned in practice by the Capability→Architecture Map's package
assignments.

### 3. Nothing under Deferred could let two units diverge — **adequate**

Safe deferrals: error-code catalog contents (single registry per AD-7 prevents shape drift), CSP
(kernel-owned static serving), `user`/`auth` package split (bounded by AD-1..AD-3), feature-internal
layout (bounded by AD-1..AD-3), the addendum API-contract questions (AD-4/AD-5 fix the invariant either
way).

Two deferrals carry divergence risk — see F2 (temp-file/fsync strategy deferred "with implementation"
while several independent slices write files) and F5 (per-route API contracts deferred per slice with
no pinned cross-slice query/pagination/filter conventions).

### 4. Named tech is verified-current or explicitly repo-pinned — **strong**

Every Stack entry traces to a repo pin: Go ≥1.27.0 and module path (AGENTS.md), Echo 5.3.1 (AGENTS.md,
`.agents/rules/echo.md`), `modernc.org/sqlite` + golang-migrate v4 (AGENTS.md), CookieStore sessions
(AGENTS.md), tiktoken-go `o200k_base` (docs/architecture.md:178-179, addendum:16), SecLists top-100k
(docs/architecture.md:76), `gpt-5.6-terra` default (docs/server-configuration.md:251,262; addendum:71),
`mistral-ocr-4-1` fixed (docs/server-configuration.md:272). "Current at implementation" entries are
explicit rather than fabricated versions — correct posture for a pre-code repo.

### 5. Ratifies rather than contradicts brownfield reality — **strong**

Spot-checked `[ADOPTED]` ADs against repo docs: AD-9 matches AGENTS.md exactly (WAL, `synchronous=FULL`,
FK on, 5 s busy timeout, 4/4 pool, `mia.lock` before open, embedded forward-only migrations). AD-8's
security-generation cookie invalidation matches docs/database-layout.md:142,174-177. AD-11's startup
order matches AGENTS.md and NFR-15. AD-12's random one-way fingerprints match
docs/database-layout.md:1242-1269. Conventions match repo sources: exactly-6-fractional-digit instants
(docs/database-layout.md:57-58), `cases.Fold`+NFC / no `NOCASE` (docs/database-layout.md:84-86),
Argon2id (docs/database-layout.md:186, NFR-4), `public_url`-only emailed links
(docs/server-configuration.md). One trivial seed tension noted as F7.

### 6. Covers the driving PRD's capabilities — **adequate**

The Capability→Architecture Map covers FR-1..80 and FR-83..84 in contiguous ranges with sensible
package and AD assignments. Gaps: FR-81 and FR-82 are absent from the map (F4); FR-81 (instants +
user time zone) is substantively covered by the Instants convention but unmapped; FR-82 (no workflow
notifications) is a negative requirement with no home. NFR coverage is real but mostly implicit:
NFR-1/2 (AD-4), NFR-4/6/7/21 (conventions/seed), NFR-9..11 (map row), NFR-14/15 (AD-11), NFR-16 (AD-5),
NFR-18 (AD-10), NFR-19/20 (seed/AD-9) — yet the map itself carries only one NFR row, and NFR-3, NFR-5,
NFR-12's fixed token budgets, and NFR-13's bounds discipline appear nowhere in the spine body (F4, F8).

### 7. Every altitude-owned dimension decided, deferred, or open — **thin**

This is the weakest area. Decided or visible: deployment topology (reverse proxy, listener, doc_root —
Structural Seed diagram), single-process/single-node posture, config validation, upgrade data-safety
(AD-9 dirty/newer-schema refusal), startup integrity (AD-11). Silent dimensions:

- **Operations/observability**: no AD, convention, or deferral covers logging (F1) — despite the repo
  already pinning a `[log]` config table (file/level/format json|text, SIGHUP reopen —
  docs/server-configuration.md:194-229). Health/metrics endpoints are legitimately absent (none exist
  in repo docs), but the spine should say so rather than be silent.
- **Backup/restore**: NFR-14 ("one consistent data set, backed up and restored together") is cited only
  as an AD-11 binding; the spine never states the backup/restore posture or the operator boundary (F3).
- **Upgrade story**: binary replacement/rollback narrative is implicit only (F6).
- **Environments**: no dev/test/prod statement; for a self-hosted single binary this is near-N/A, but a
  one-line disposition belongs at initiative altitude (folded into F6).

### 8. Terse and convergent — **strong**

Decisions, not rationale; "Prevents" lines are one clause each. Shape carried by two diagrams; both
mermaid blocks are syntactically valid (`graph TD`/`graph LR`, quoted multiline labels, labeled dotted
edges). 268 lines for a 12-AD whole-system spine is lean. Deferred section is disciplined — each entry
names the trigger point and the AD that bounds it. No bloat found.

## Findings

### F1 — Logging/observability dimension is silent — **high**

- Location: entire spine (no AD, no convention row beyond Redaction, no Deferred entry); contrast
  docs/server-configuration.md:194-229 (`[log]` file/level/format, SIGHUP reopen).
- Risk: each feature slice picks its own logger, levels, and field conventions; the repo-confirmed
  json/text encodings and reopen behavior go unratified. This is a real per-slice divergence point at
  exactly the altitude the spine owns.
- Fix: add a convention row or short AD ratifying the `[log]` contract (single shared logger from the
  kernel, structured fields, level discipline) or an explicit Deferred entry with a named owner.

### F2 — File-write machinery has no single owner — **medium**

- Location: AD-11; Deferred ("Temporary-file placement and fsync/directory-sync strategy — specified
  with implementation"); Structural Seed (no file-storage kernel package).
- Risk: `material`, `speech`, `user` (avatars), and `course` (logos) all write data-directory files.
  AD-11 fixes the atomic-rename contract, but the mechanism (temp placement, fsync, dir-sync) is
  deferred "with implementation" — plural implementations means plural strategies. Two slices can
  satisfy AD-11 with incompatible temp-file conventions that startup reconciliation must then
  understand.
- Fix: name the owner — e.g., "one shared file-publication helper in the kernel, added with the first
  file-writing slice; later slices use it" — keeping the strategy deferred but the ownership fixed.

### F3 — Backup/restore posture unstated — **medium**

- Location: AD-11 binds line (NFR-14 cited); no body text anywhere on backup/restore.
- Risk: low divergence risk for slices, but the checklist dimension is genuinely absent: nothing states
  that backup = SQLite + data dir together, that the operator owns it, or what a consistent snapshot
  requires under WAL (docs/data-dir.md:11-16,70 already treats backups as part of the trust boundary).
- Fix: one sentence ratifying NFR-14 and the operator boundary, or an explicit Deferred/open entry.

### F4 — Capability map gaps: FR-81, FR-82, and most NFRs — **medium**

- Location: Capability → Architecture Map (rows cover FR-1..80, FR-83..84 only; single NFR row for
  NFR-9..11).
- Risk: FR-81 is covered by the Instants convention but untraceable from the map; FR-82 (no workflow
  notifications) has no home at all — a slice could "helpfully" add notification behavior. NFR-3
  (untrusted-input posture), NFR-12 (32k/2k token budgets), and NFR-13 (bounds discipline) appear
  nowhere in the spine despite `binds: FR-1..84, NFR-1..21` in frontmatter.
- Fix: add rows (or a conventions-mapped row) for FR-81..82 and the unmapped NFRs; state NFR-12's fixed
  budgets in the tutoring row or AD-10.

### F5 — Per-route API-contract deferral lacks pinned cross-slice query conventions — **medium**

- Location: Deferred, first bullet ("Per-route API contracts … defined per vertical slice").
- Risk: JSON:API + kebab/snake casing (Conventions) constrains resource shape, but pagination,
  filtering syntax, sparse-fieldset support, and sorting are left to each slice. Two slices defining
  list endpoints independently can diverge on query grammar within the same API.
- Fix: pin the query-parameter grammar (or explicitly designate `docs/api.md` as the binding convention
  source, not just "the draft") before the second list-bearing slice.

### F6 — Upgrade/deployment narrative implicit only — **low**

- Location: AD-9 (dirty/newer-than-executable refusal), Structural Seed diagram.
- Risk: data-safety on upgrade is covered; the operational sequence (stop, replace binary, start;
  `mia.lock` prevents overlap; no environments story) is implied but never stated. One or two sentences
  close the dimension.

### F7 — `audit/` in the initial structural seed vs AGENTS.md initial-package list — **low**

- Location: Structural Seed (`audit/` listed unconditionally); AGENTS.md ("Initial packages cover only
  command wiring, configuration, locking, SQLite/migrations, identity, HTTP, and the first auth
  slice").
- Risk: minimal — the auth slice audits security actions (FR-79), so `audit` plausibly arrives with it;
  but the seed should mark it "added with the first audited slice" to match the ratified initial list,
  as it already does for `provider/*` and `jobs/`.

### F8 — NFR-5 plaintext-storage posture unstated — **low**

- Location: Conventions (Credentials row covers hashing/digests only); no mention of the confirmed
  no-field-encryption posture for TOTP secrets and SMS codes (NFR-5, AGENTS.md).
- Risk: a slice implementer could add ad-hoc field encryption, diverging from the confirmed MVP posture
  and complicating backup/restore. One clause in the Credentials or Redaction row settles it.

## Summary

| Item | Verdict |
| --- | --- |
| 1. Divergence points fixed | adequate |
| 2. Rules enforceable | strong |
| 3. Deferrals safe | adequate |
| 4. Tech verified/pinned | strong |
| 5. Ratifies brownfield | strong |
| 6. PRD capability coverage | adequate |
| 7. Dimensions decided/deferred/open | thin |
| 8. Terse and convergent | strong |

Findings: 0 critical, 1 high, 4 medium, 3 low. The spine is a genuinely strong build substrate for
vertical slices — its weakness is the operational/environmental envelope, which is mostly repairable
with a handful of ratifying sentences rather than new decisions.
