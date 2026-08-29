# Review — Technology Reality Check (Stack table and technology-naming ADs)

- Artifact: `_bmad-output/planning-artifacts/architecture/architecture-mia-2026-08-29/ARCHITECTURE-SPINE.md`
- Scope: Stack table (lines 178–192), AD-8, AD-9, AD-10, Consistency Conventions rows naming technology
- Review date: 2026-08-29
- Method: live web verification (pkg.go.dev, GitHub) plus cross-check against the repository's confirmed
  decisions in `docs/architecture.md`, `docs/product-requirements.md`, `docs/server-configuration.md`,
  `AGENTS.md`, and `.agents/rules/echo.md`. Repo-pinned versions are treated as authoritative.

## Verdict

Every committed technology in the spine exists, is alive, fits its stated use, and matches the repository's
own pins; no spine entry contradicts a repo pin. Findings below are fit-and-precision hardening, not
existence or version errors.

## Verification results per stack entry

| Entry | Spine claim | Verified | Evidence |
| --- | --- | --- | --- |
| Go | ≥ 1.27.0, module `github.com/thorstenkramm/mia` | Match (repo pin) | `docs/architecture.md` Go Baseline; `AGENTS.md`. Repo is authority for the 1.27.0 floor. |
| Echo | 5.3.1 | Exists | pkg.go.dev shows `github.com/labstack/echo/v5` v5.3.1 published Jul 21, 2026 (MIT, tagged, stable). Matches repo pin in `AGENTS.md` and `.agents/rules/echo.md`. Note: pkg.go.dev flags 5.3.1 as "not the latest" — a newer 5.x patch exists. |
| modernc.org/sqlite | current at implementation; CGo-free, WAL, `synchronous=FULL`, FK, 5 s busy timeout (AD-9) | Fit confirmed | pkg.go.dev: v1.57.0 published Aug 19, 2026; pure-Go `database/sql` driver wrapping SQLite 3.53.3; pragmas (WAL, synchronous, foreign_keys, busy_timeout) configurable via `_pragma` DSN parameters. Linux amd64/arm64 and darwin supported. |
| golang-migrate | v4, library, embedded migrations | Fit confirmed | pkg.go.dev: module v4.19.1 published Nov 29, 2025. `source/iofs` accepts `embed.FS` (matches `docs/architecture.md` "embedded through `io/fs`"). `database/sqlite` driver explicitly "Uses the modernc.org/sqlite sqlite db driver (pure Go)" — coherent with AD-9. |
| Gorilla sessions (CookieStore via Echo) | current at implementation (AD-8) | Fit confirmed with a precision gap | The Echo-v5-compatible path exists: `github.com/labstack/echo-contrib/v5/session` v5.0.1 published Feb 19, 2026, with the Echo 5 `*echo.Context` signature and gorilla `sessions.Store`. The non-`/v5` echo-contrib module (v0.50.1) targets Echo v4 and will not compile against `echo/v5`. See M-2. |
| tiktoken-go/tokenizer | current; `o200k_base` encoding | Exists, encoding supported | GitHub `tiktoken-go/tokenizer` README: pure-Go tiktoken port; `o200k_base` listed as implemented. Matches repo pin `github.com/tiktoken-go/tokenizer` in `docs/architecture.md`. See M-1 for the model-mapping caveat. |
| golang.org/x/text | current (BCP 47, `cases.Fold`) | Exists | pkg.go.dev: x/text v0.41.0 published Aug 11, 2026; `cases.Fold()` present (Unicode 17.0.0 tables); `x/text/language` provides BCP 47 per `docs/architecture.md`. |
| SecLists common passwords | top-100k, embedded, version recorded on addition | Exists | GitHub `danielmiessler/SecLists` `Passwords/Common-Credentials/` contains `xato-net-10-million-passwords-100000.txt` (and also `100k-most-used-passwords-NCSC.txt`). Matches `docs/architecture.md` and PRD ("bundled SecLists top-100,000"). See L-2. |
| OpenAI `gpt-5.6-terra` | default chat/job, Responses API + function calling | Match (repo pin) | Pinned in `docs/server-configuration.md` (defaults) and `docs/product-requirements.md` line 1368 (Responses API + tools requirement). Not independently verifiable against OpenAI's live catalog from here; repo is authority. |
| Mistral OCR `mistral-ocr-4-1` | fixed | Match (repo pin) | Pinned in `docs/architecture.md` Provider Operations. Repo is authority. |

No spine entry contradicts a repo pin. The Stack table is internally consistent with AD-8/AD-9/AD-10 and
with the Consistency Conventions (Argon2id, SHA-256 token digests, `cases.Fold`+NFC keys).

## Findings

### High

None.

### Medium

- **M-1 — `o200k_base` for `gpt-5.6-terra` is an assumption the spine states as settled.**
  `tiktoken-go/tokenizer` verifiably implements `o200k_base`, but no public source available to this review
  confirms that `o200k_base` is the correct encoding for the pinned 2026 model `gpt-5.6-terra`, and the
  library's built-in model→encoding map will not know that model name. `docs/architecture.md` already
  requires "unsupported model-tokenizer mapping is a startup configuration error"; the spine's Stack row
  should carry the same qualifier (encoding verified against the provider's tokenizer documentation at
  adapter implementation) rather than presenting `o200k_base` as final. Secondary caveat from the same
  README: special tokens are not handled, so local counts can undercount slightly versus the provider —
  acceptable for a budget check only if the budget is treated as approximate or given headroom.

- **M-2 — Session row should name the exact integration module (`echo-contrib/v5`).**
  "Gorilla sessions (CookieStore via Echo) — current at implementation" is real and verified, but only via
  `github.com/labstack/echo-contrib/v5/session` (v5.0.1). The default `github.com/labstack/echo-contrib`
  module (v0.x line, session v0.50.1) is built against Echo v4's interface-based `echo.Context` and is
  incompatible with the pinned Echo 5.3.1. "Current at implementation" without the module path invites
  pulling the wrong major. One-word fix in the Stack table removes the trap.

### Low

- **L-1 — modernc.org/sqlite's documented fragile `modernc.org/libc` coupling is not surfaced.**
  The driver's own docs state the importer should use the exact `modernc.org/libc` version from the driver's
  `go.mod`. "Current at implementation" is fine, but the AD-9 contract (or the Stack row) should note the
  libc co-pin so a future dependency bump doesn't silently violate it.

- **L-2 — "SecLists top-100k" is ambiguous between two real files.**
  `Passwords/Common-Credentials/` contains both `xato-net-10-million-passwords-100000.txt` and
  `100k-most-used-passwords-NCSC.txt`. The repo docs say "top-100,000 common-password list" without naming
  the file. Since `docs/architecture.md` requires recording the upstream version and license on addition,
  recording the exact filename + commit at that time closes this; the spine could pre-empt it by naming the
  intended file.

- **L-3 — Echo 5.3.1 is no longer the newest 5.x patch.**
  pkg.go.dev marks 5.3.1 (Jul 21, 2026) as not-latest. The repo pin is authoritative and is not flagged as
  wrong; recommend a deliberate patch-review before first `go.mod` creation so the pin is a choice, not
  drift.

- **L-4 — "Current at implementation" entries are acceptable here, with one guardrail.**
  For modernc.org/sqlite, gorilla sessions, tiktoken-go, and x/text, deferring the exact version to `go.mod`
  resolution at implementation is reasonable — all four are verified alive and release regularly
  (Aug 2026, Feb 2026, active, Aug 2026 respectively). The guardrail: the versions become de-facto pins in
  `go.mod`/`go.sum` on first commit, which satisfies reproducibility; no spine change required beyond M-1/M-2.

### Informational

- AD-8's "signed+encrypted" CookieStore requires configuring both a securecookie hash key and a block key;
  hash-key-only CookieStore is signed but not encrypted. Worth a line in the auth slice's contract.
- golang-migrate's sqlite driver wraps each migration in an implicit transaction by default (migrations must
  not contain explicit BEGIN/COMMIT unless `x-no-tx-wrap` is used) — relevant when writing the first
  migrations.
- `tiktoken-go/tokenizer` is a small project (~451 stars, 44 commits) with vocabularies embedded at build
  time (~4 MB binary size impact) — consistent with the self-hosted single-binary design, noted for
  awareness only.

## Counts

| Severity | Count |
| --- | --- |
| High | 0 |
| Medium | 2 |
| Low | 4 |
| Informational | 3 |
