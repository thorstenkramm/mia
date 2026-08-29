# Reconciliation: ops/config/data sources vs PRD + addendum

Scope: `docs/architecture.md`, `docs/server-configuration.md`, `docs/data-dir.md`,
`docs/database-layout.md` (skimmed) compared against `prd.md` and `addendum.md`.

Verdict: no contradictions found; all shared numeric limits, defaults, provider
roles/optionality, and startup semantics match. Several operator-visible
behaviors and defaults confirmed by the sources are captured by neither the PRD
nor the addendum.

## Findings (ordered by severity)

### F1 — `main.public_url` security contract missing (moderate)

Source: `docs/server-configuration.md` §`main.public_url`.
The externally visible origin is a mandatory setting, must be HTTPS with no
credentials/path/query/fragment, and invitation and password-reset links are
derived exclusively from it — MIA never derives security-sensitive links from
request or forwarded headers. PRD FR-14 documents the link fragment format but
neither PRD §7.1 (Security) nor the addendum captures the "never derived from
request headers" rule or that the origin is a mandatory HTTPS-only operator
setting. This is a user-visible security behavior (link host) and belongs at
least in the addendum, arguably in NFR-4/NFR-8.

Affected: PRD §6.2 FR-14, §7.1; addendum "Technology stack".

### F2 — Operator-visible configuration defaults not captured (moderate)

Source: `docs/server-configuration.md`.
Confirmed defaults visible to every operator are absent from both documents:

- Default config file `/etc/mia/mia.toml` (optional); `--config` uses an exact
  path that must exist.
- `http.listen` default `127.0.0.1:9900`; alternative Unix endpoint
  (`unix:/…`, socket mode `0660`, `http.socket_group`).
- Logging defaults: stderr when `log.file` unset, level `info`, format `json`
  (`json`/`text`, `debug|info|warn|error`).
- SMTP defaults: port `587`, transport `starttls` (values `starttls`,
  `implicit_tls`, `plaintext` with explicit operator-risk warning).
- Startup config failure goes to standard error with exit code 1.

Affected: addendum "Technology stack"; PRD §9 (Operator-Configurable Behavior).

### F3 — Unsupported model-tokenizer mapping is a startup error (minor)

Source: `docs/architecture.md` "Tutor Context And Retrieval".
"An unsupported model-tokenizer mapping is a startup configuration error."
PRD §9 says the chat and job models are configurable and NFR-12 requires a
matching local tokenizer, but neither states that configuring a model without a
known tokenizer mapping prevents startup. This materially constrains the
"configurable model" claim for operators.

Affected: PRD §9, NFR-12/NFR-20; addendum "Provider deadlines / Default models".

### F4 — Instruction-file edits require a restart (minor)

Source: `docs/data-dir.md` (intro) and `docs/architecture.md`
"Instruction Defaults". Operators may edit the `llm-instructions/` prompt
files, but changes take effect only after a restart; startup creates missing
files independently and never overwrites existing ones. The addendum notes
"MIA creates them but never overwrites" but omits the restart requirement,
which is the operator-visible half of the contract.

Affected: addendum "Data directory layout"; PRD §9.

### F5 — `uploads.max_material_size_mib >= uploads.max_file_size_mib` (minor)

Source: `docs/server-configuration.md` §`uploads.max_material_size_mib`.
The combined-material limit must be greater than or equal to the single-file
limit. PRD FR-50 lists both defaults and caps but omits this cross-field
validation constraint, which operators will hit when tuning limits.

Affected: PRD FR-50; addendum (none).

### F6 — Offline commands' configuration subset stated but incomplete (info)

Source: `docs/server-configuration.md` intro and `docs/architecture.md`
"Process lock and local commands". NFR-19 says offline commands "validate only
their required configuration subset"; the sources add that they still reject
malformed input and unknown keys and require no doc root or provider settings.
Low impact; consider one clarifying clause in NFR-19 or the addendum.

Affected: PRD NFR-19.

## Verified matches (no action)

- Upload defaults/caps (100/512 MiB file, 200/512 MiB material, 1,000/2,000
  pages, 200/200 files, 40/100 MP, 20,000 px cap) — FR-50 matches
  server-configuration.md exactly.
- Speech retention 1–365 days, default 30; access does not extend retention —
  FR-73, PRD §9, data-dir.md agree.
- Provider requirement/optionality (OpenAI, Mistral, SMTP required; ClickSend,
  ElevenLabs optional, absent table disables feature) — PRD §8 matches.
- Startup order (expired speech → stranded generation failed → fail hard on
  missing referenced files; avatars/logos optional) — NFR-15 matches
  architecture.md/data-dir.md.
- SQLite settings, pool 4/4, lock file, migrations, HTTP/provider deadlines,
  SSE heartbeat/write deadline, session.key semantics — addendum matches.
- Avatar/logo limits (10 MiB, 40 MP, 10,000 px, 512 px output PNG) — FR-36
  matches data-dir.md.
- Certificate verification never disableable; secrets never via flags; SIGHUP
  reopens log only — PRD §9, NFR-4, NFR-20 match.
