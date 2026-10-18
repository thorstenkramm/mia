---
title: 'Ignore nonconforming PDF document metadata during material processing'
type: 'bugfix'
created: '2026-10-18'
status: 'done'
baseline_commit: '0e0d375363915538b271c1c95f856317ecde2b99'
review_loop_iteration: 0
context:
  - '{project-root}/_bmad-output/implementation-artifacts/spec-relaxed-pdf-validation.md'
---

<frozen-after-approval reason="human-owned intent — do not modify unless human renegotiates">

## Intent

**Problem:** pdfcpu relaxed validation still rejects document-information entries such as `/Title 123` and
`/Trapped /Invalid`. This contradicts the approved requirement that document metadata conformance must not block
material upload or processing. The user selected broader tolerance rather than narrowing that contract.

**Approach:** Detach document-information and catalog-level XMP metadata from the parsed processing context before
validation. Validate the remaining document and extract OCR chunks from that context. Retain the uploaded source
bytes unchanged. This exception concerns document metadata semantics, not unreadable PDF syntax or broken content.

## Boundaries & Constraints

**Always:** Keep signature and raw hazard-marker checks before metadata detachment. Reject encryption, unreadable
documents, zero-page documents, and existing size/page/file-count limit violations. Use the existing deterministic
pdfcpu configuration helper. Keep all nonmetadata validation. Preserve page content, order, and the 25-page OCR
chunk boundary. Preserve current stable errors and parser resource bounds. Use synthetic fixtures and local fakes.

**Ask First:** Changes to upload limits, hazard markers, dependency versions, API errors, or persisted source bytes.
Any metadata exception beyond document-level Info and catalog XMP requires clarification.

**Never:** Do not use `ValidationNone`, match parser error strings, swallow unrelated validation failures, or rewrite
PDF syntax with regular expressions. Do not delete shared referenced objects when detaching metadata references.
Do not call external providers in tests or log metadata/parser error payloads.

## I/O & Edge-Case Matrix

| Scenario | Input / State | Expected Output / Behavior | Error Handling |
|----------|---------------|----------------------------|----------------|
| Bad Info | Numeric title, invalid Trapped, bad dates, combined | Upload and processing succeed | None |
| Bad document XMP | Parseable PDF with invalid catalog metadata | Upload and processing succeed | None |
| Multiple chunks | 26+ distinguishable synthetic pages | Correct chunks and complete ordered content | None |
| Metadata hazard | Existing forbidden marker in Info/XMP | Rejected before detachment | Existing invalid error |
| Invalid structure | Unreadable xref, missing page tree, zero pages | Rejected | Existing invalid error |
| Encryption or limits | Encrypted PDF or existing bounds exceeded | Rejected | Existing invalid error |

</frozen-after-approval>

## Code Map

- `internal/material/validation.go:201-271` — PDF upload branch and deterministic configuration helper. Currently
  Validate and PageCount both inspect original Info. Return original bytes after obtaining validated page count.
- `internal/material/jobs.go:127-154` — OCR uses api.Trim, which rereads and validates original metadata. Replace
  that path with validated-context extraction; preserve usage accounting and sequence assignment.
- `internal/material/pdf_test.go` — existing synthetic builder, rejection matrix, and worker-driven happy path.
- `internal/material/material.go:170-246` — source persistence and aggregate upload limits; preserve semantics.
- pdfcpu v0.15.0 `pkg/api/api.go` — ReadContext, ValidateContext, OptimizeContext, WriteContext; lower-level context
  calls need the same `fault.Catch` boundary used by public convenience APIs.
- pdfcpu `pkg/api/trim.go` — use its preparation/extraction/write sequence as the integration reference, including
  TRIM configuration, optimization/font preparation and destination validation. Do not copy file/CLI plumbing.
- pdfcpu `pkg/pdfcpu/validate/info.go` and `metaData.go` — Info nil bypasses Info validation; detach catalog Metadata
  before validation. Merely selecting relaxed mode is insufficient.

## Tasks & Acceptance

**Execution:**
- [x] `internal/material/validation.go` and a focused PDF helper file if useful — share parse, metadata detachment,
  encryption rejection, validation and page-count handling between upload and OCR.
- [x] `internal/material/jobs.go` — generate chunks from the prepared context without reparsing original metadata;
  follow the existing pdfcpu extraction semantics and propagate failures through existing error handling.
- [x] `internal/material/pdf_test.go` — cover every matrix row, unchanged source bytes, independently validated OCR
  chunks, recognizable page content, ordering across chunk boundaries, and configuration independence.
- [x] `docs/product-requirements.md`, `docs/jobs.md`, `docs/database-layout.md` — explain the document-level metadata
  exception and unchanged source retention. Reconcile the existing deferred Info-forwarding entry if resolved.
- [x] Original review spec and this spec — record resolution and verification evidence without rewriting prior intent.

**Acceptance Criteria:**
- Given parseable PDFs with the specified metadata defects, when uploaded and finalized, then they reach ready
  with correct page counts and ordered extracted content using local OCR and summary fakes.
- Given an accepted PDF, when downloaded after processing, then its source bytes equal the uploaded bytes.
- Given safety or structural rejection cases, when metadata tolerance is applied, then existing rejections persist.
- Given the completed change, when the full project gate runs, then all components pass or unavailable checks are named.

## Spec Change Log

## Design Notes

Implementation uses `internal/material/pdf.go` for a shared `readMaterialPDF` boundary and context-based chunk
preparation/writing. Each lower-level pdfcpu operation is covered by `fault.Catch`, retaining the library's parser
bounds and normal nonmetadata validation. OCR prepares and optimizes a fresh context for every chunk, caches form
fonts, extracts ascending page selections, and applies pdfcpu's configured destination validation before writing.
The parsed page count is checked against the persisted count before any OCR call. Reusing a context is unsafe:
pdfcpu's `ExtractPages` mutates source named-destination trees, including removing unused destination object graphs.

The shared-stream regression points catalog Metadata at the first page's content stream. Its page content survives
detachment, proving that referenced objects are retained. The OCR fake independently reads and validates emitted
PDFs, reads actual page streams, checks exact chunk sizes, and verifies the output document-level author/title/XMP
references are absent for those fixtures. Shared objects can still reach OCR through content references.

Review follow-up: a synthetic 27-page PDF with two named-destination leaves reproduced context reuse losing page 27
(`extract pages [26 27]: add pages: page 27: read page dict: page not found`). The same regression passes with
independent chunk contexts and verifies complete ordered content and 25/2 chunk sizes. A persisted count of 1 for
an actual two-page source returns `material_ocr_invalid`, with no OCR calls, usage, result content, or content file.

The historical Info-forwarding claim was not reproducible with original pdfcpu v0.15.0 `api.Trim` and a valid
synthetic Info dictionary: output author/title were absent and producer replaced. The deferred entry is corrected
as an inaccurate review inference, not a forwarding bug fixed by this change. The metadata-validation fix stands.

Prefer direct context-based page extraction over a full intermediate PDF rewrite: it avoids an additional
whole-document buffer and a new processing-copy size policy. Metadata references are detached, not recursively
deleted, because objects may be shared with content. Generated PDFs may contain pdfcpu's own producer metadata;
this is not a general metadata-removal or anonymization feature.

## Verification

Review patches verified on 2026-10-18: `go test ./internal/material/... -count=1` and `./run-all-tests.sh` both
passed after adding independent per-chunk contexts and the named-destination, persisted-count mismatch, and original
Trim regressions. The full gate ran all components, including race tests, zero lint issues, zero duplication clones,
Redocly API lint, and Markdown lint; no checks were skipped. Vulnerability results remain as recorded below.

- `go test ./internal/material/...` — passed on 2026-10-18, including every matrix row and worker-driven ready-state
  checks for all metadata cases. The 27-page case verifies exact 25/2 chunks, page text and sequence, and original
  source download bytes. Aggregate file-count, page-count and byte limits are also covered.
- `./run-all-tests.sh` — passed on 2026-10-18: gofmt, all Go tests, vet, golangci-lint (0 issues), race tests,
  duplication marker balance, JSCPD (0 clones), govulncheck, Trivy, Redocly API lint, and Markdown lint. No checks
  skipped. Govulncheck reports no reachable vulnerabilities and one vulnerability in a required module outside
  called code; Trivy reports zero unsuppressed vulnerabilities/secrets under the existing repository policy.
- `git diff --check` — passed. No external OCR or summary providers were called.

## Suggested Review Order

**Validation boundary**

- Detach document metadata while retaining structural validation and shared objects.
  [`pdf.go:13`](../../internal/material/pdf.go#L13)
- Preserve uploaded bytes and use the validated page count.
  [`validation.go:205`](../../internal/material/validation.go#L205)

**OCR extraction**

- Isolate mutable pdfcpu state per chunk and reject inconsistent page counts.
  [`jobs.go:126`](../../internal/material/jobs.go#L126)
- Follow pdfcpu preparation and extraction with parser-fault handling.
  [`pdf.go:42`](../../internal/material/pdf.go#L42)

**Contract and evidence**

- Document metadata tolerance, retained source bytes, and shared-content limitations.
  [`product-requirements.md:1120`](../../docs/product-requirements.md#L1120)
- Exercise real page content, multiple chunks, source retention, and rejection boundaries.
  [`pdf_test.go:109`](../../internal/material/pdf_test.go#L109)
