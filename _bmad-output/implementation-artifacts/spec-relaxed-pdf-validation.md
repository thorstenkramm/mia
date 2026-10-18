---
title: 'Relaxed, deterministic PDF validation for material uploads'
type: 'bugfix'
created: '2026-10-18'
status: 'done'
baseline_commit: '63c99925e5995fe2e2467f4f894976dfd4c0d297'
review_loop_iteration: 0
context:
  - '{project-root}/tmp/frontend-request-relaxed-pdf-validation-2026-10-18.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** Material PDF upload uses pdfcpu strict validation, so common real-world PDFs with nonconforming
document-information metadata (for example `CreationDate` `D:20250809135053Z`) are rejected as `422
material_invalid`. pdfcpu configuration also comes from a `pdfcpu/config.yml` it creates under the server user's
`os.UserConfigDir()`, outside MIA's configuration and data directory.

**Approach (product owner decision 2026-10-18):** Use pdfcpu relaxed validation for upload validation, page
counting, and OCR page splitting, and disable pdfcpu's configuration directory so every MIA pdfcpu call uses one
explicit, environment-independent configuration. Metadata conformance must not block upload, use, or processing.

## Boundaries & Constraints

**Always:** Keep the `%PDF-` signature check and `containsPDFHazard` marker rejection unchanged. Keep encrypted PDFs,
unparseable documents, zero-page documents, and operator file-size, page, and file-count limits rejected. Route
every pdfcpu configuration in `internal/material` through one helper. Synthetic, non-copyrighted test PDFs only; no
provider calls.

**Ask First:** Any OpenAPI or error-code change; any change to the hazard marker list or limits.

**Never:** No new stable error code (deferred follow-up). No pdfcpu version change. No `t.Parallel()` in tests that
set environment variables.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|--------------|---------------------------|----------------|
| Nonconforming info date | 2-page PDF, Info `CreationDate`/`ModDate` `(D:20250809135053Z)` | Upload accepted, `page_count` 2; finalize + OCR fake → material `ready` | N/A |
| Hazard marker | Valid PDF containing each of `/Encrypt`, `/EmbeddedFile`, `/JavaScript`, `/JS `, `/Launch` | Rejected | `ErrInvalid` |
| Unparseable | `%PDF-1.4` + garbage; corrupt xref subsection header | Rejected | `ErrInvalid` |
| Zero pages | Page tree with `/Count 0` | Rejected | `ErrInvalid` |
| Limits | Size above `MaxFileBytes`; pages above `MaxPages` | Rejected | `ErrInvalid` |
| No signature | Valid PDF body without leading `%PDF-` | Rejected | `ErrInvalid` |

</frozen-after-approval>

## Code Map

- `internal/material/validation.go:200-219` -- `validateSource` PDF branch: `model.NewDefaultConfiguration()` +
  `ValidationStrict` for `api.Validate` and `api.PageCount`.
- `internal/material/validation.go:293-301` -- `containsPDFHazard`; read-only.
- `internal/material/jobs.go:128-155` -- `ocrPDF` uses `model.NewDefaultConfiguration()` for `api.Trim` 25-page
  chunks (strict mode would also fail Trim on the bad date — verified).
- `internal/material/material.go:191-197` -- `Upload` calls `validateSource` and enforces cumulative page limit.
- `internal/material/material_test.go:37-41,147-250,370-425` -- `ocrFake`, worker-driven processing pattern,
  `materialFixture` (limits: 1 MiB file, 10 pages).
- pdfcpu v0.15.0 `model.NewDefaultConfiguration` reads/creates `UserConfigDir()/pdfcpu/config.yml` unless
  `model.ConfigPath == "disable"`; `api.DisableConfigDir()` sets that global under a mutex, but reads of
  `ConfigPath` are unguarded.
- `docs/product-requirements.md:1119-1121` -- PDF parser contract; stays true.

## Tasks & Acceptance

**Execution:**
- [x] `internal/material/validation.go` -- add `pdfConfiguration()` that runs `api.DisableConfigDir` once via a
  package `sync.Once`, then returns `model.NewDefaultConfiguration()` with `ValidationMode =
  model.ValidationRelaxed`; use it in `validateSource` -- one deterministic mode, race-free global write.
- [x] `internal/material/jobs.go` -- use `pdfConfiguration()` in `ocrPDF` -- OCR splitting matches upload mode.
- [x] `internal/material/pdf_test.go` -- add a hand-built synthetic PDF helper with correct xref offsets and tests
  for every I/O matrix row; the happy path goes through `Upload`, `Finalize`, and the job worker with an `ocrFake`
  returning per-chunk page Markdown until material state `ready` -- regression evidence.
- [x] `internal/material/pdf_test.go` -- config-dir test: `t.Setenv` `HOME` and `XDG_CONFIG_HOME` to a fresh temp
  dir, validate a PDF, assert the temp dir stays empty -- proves no config dir read/write.
- [x] `docs/product-requirements.md` -- after the bounded-parser sentence, state that nonconforming document
  metadata such as malformed information-dictionary dates does not cause rejection -- contract update.

**Acceptance Criteria:**
- Given a PDF rejected only by strict metadata checks, when a supervisor uploads it, then it is accepted with the
  correct page count and processes to `ready` with a local OCR fake.
- Given any MIA pdfcpu call, when it runs, then it uses relaxed mode and never touches the user configuration
  directory.
- Given `./run-all-tests.sh`, when run, then all components pass (or skipped ones are named).

## Design Notes

### Review Findings

- [x] [Review][Decision] Relaxed validation does not meet the blanket metadata-tolerance contract (medium) —
  `internal/material/validation.go:270` selects relaxed mode, but pdfcpu v0.15.0 still rejects otherwise parseable
  synthetic PDFs with `/Title 123` or `/Trapped /Invalid` in their Info dictionary. Reproduced during review of
  commit `0e0d375`; the bad-date fixture succeeds under the same configuration. This conflicts with the intent
  that metadata conformance must not block upload and the new promise in `docs/product-requirements.md:1122`.
  Decide whether to implement broader metadata tolerance or explicitly narrow the approved contract to the
  nonconformance tolerated by pdfcpu relaxed mode. No implementation change made during review.
  Resolved by `spec-pdf-document-metadata-tolerance.md`: the owner selected broader tolerance. Upload and OCR now
  detach document Info and catalog XMP references before validating the remaining context. Tests cover numeric
  titles, invalid Trapped values, bad dates, invalid catalog metadata, shared objects, ordered multi-chunk content,
  unchanged downloaded source bytes, and the existing safety/limit rejections.

`sync.Once` publishes the `ConfigPath` write before any `NewDefaultConfiguration` read through the helper, keeping
`go test -race` clean. Verified locally: the synthetic bad-date PDF fails strict Validate/PageCount/Trim and passes
relaxed with the correct page count; garbage, corrupt xref header, and truncated bodies fail in both modes; a wrong
`startxref` offset is repaired in both modes (no behavior change).

## Verification

Follow-up metadata-tolerance implementation verified 2026-10-18: `go test ./internal/material/...` and
`./run-all-tests.sh` passed, including race, zero duplication clones, Redocly API lint, and Markdown lint.
All checks ran. Govulncheck reports no reachable vulnerabilities (one required-module vulnerability outside
called code); Trivy reports zero unsuppressed findings with the existing policy.

Follow-up review evidence: `TestOriginalPDFTrimDoesNotForwardInfo` passes against original pdfcpu v0.15.0
`api.Trim` with valid synthetic title, author and producer metadata. It does not forward the source title/author
and replaces the producer. The deferred Info-forwarding entry was an inaccurate inference, now corrected;
metadata-conformance rejection remains the independently reproduced bug fixed by the follow-up spec.

**Commands:**
- `go test ./internal/material/...` -- expected: pass
- `./run-all-tests.sh` -- expected: all components pass

## Suggested Review Order

**pdfcpu configuration policy**

- Single helper: disables pdfcpu config dir once, forces relaxed validation.
  [`validation.go:267`](../../internal/material/validation.go#L267)

- Upload validation and page count now use the helper instead of strict mode.
  [`validation.go:211`](../../internal/material/validation.go#L211)

- OCR page splitting uses the same deterministic configuration.
  [`jobs.go:131`](../../internal/material/jobs.go#L131)

**Product contract**

- Metadata nonconformance explicitly no longer rejects a PDF.
  [`product-requirements.md:1122`](../../docs/product-requirements.md#L1122)

**Tests**

- Bad-date PDF fails strict, then uploads and processes to `ready`.
  [`pdf_test.go:73`](../../internal/material/pdf_test.go#L73)

- Rejection matrix: unparseable, xref, zero pages, limits, signature, hazards.
  [`pdf_test.go:129`](../../internal/material/pdf_test.go#L129)

- Order-independent config-dir assertion plus empty temp home check.
  [`pdf_test.go:179`](../../internal/material/pdf_test.go#L179)

- Hand-built synthetic PDF with computed xref offsets.
  [`pdf_test.go:23`](../../internal/material/pdf_test.go#L23)
