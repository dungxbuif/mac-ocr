# PDF text extraction policy

## Intended behavior

PDF submissions use `options.processingMode` with three values:

- `auto` (default): run `pdf-inspector` in the proxy background queue. Complete
  native-text PDFs without consuming a Mac worker slot; route scanned, image,
  mixed, empty, or failed extraction to the existing OCR queue.
- `text`: require native text extraction. If the PDF has no usable text, the
  document fails with a clear error instead of invoking OCR.
- `ocr`: always use the existing Mac OCR path.

Images continue to use OCR. The existing internal `scan` mode remains reserved
for document scanning.

## Technical decision

The proxy runs the MIT-licensed Firecrawl `pdf-inspector` `pdf2md` CLI as a
background process. It downloads the already validated object from object
storage into a private temporary file, invokes the pinned CLI with JSON and
page markers, and maps Markdown into the existing `OCRResult` contract. The
Rust dependency is built into the proxy image; no PDF parsing or model runtime
is added to Mac workers.

Text extraction claims are separate from native capacity claims. They use the
same PostgreSQL processing lease and retry lifecycle, so a Mac at zero
available units does not prevent text-only PDFs from completing. A successful
extraction stores the result in object storage and Redis and emits the same
document notification used by OCR callbacks.

## Operational policy

`PDF_INSPECTOR_BIN` selects the helper binary and defaults to
`/usr/local/bin/pdf2md`. If it is unavailable, `auto` safely falls back to
OCR and `text` fails with an unavailable-extractor error. The processing lease
must exceed the expected extraction time for large PDFs. Extraction runs in
the scheduler background and does not block HTTP submission or polling.

Validated PDFs are capped at 1,000 pages and 100 MiB. The cap protects parser
memory while allowing long reports and books to use the text-only fast path.

## Verification

Unit tests cover mode validation, capacity-independent text claims, scanned
fallback, explicit text-mode failure, and result finalization. The release
gate builds the pinned `pdf-inspector` binary, runs Go tests and vet, runs the
native Swift tests, and performs a real text-based PDF smoke test followed by a
scanned-PDF OCR fallback test.
