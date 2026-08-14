---
title: OCR response model
sidebar_position: 2
---

# OCR response model

This page defines how to consume a completed OCR result. The native macOS worker uses Apple's Vision text-recognition request and converts its observations into a stable, engine-neutral JSON model. The mapping is intentionally small so clients do not need Apple frameworks.

Apple references: [Recognizing text in images](https://developer.apple.com/documentation/vision/recognizing-text-in-images), [`VNRecognizeTextRequest`](https://developer.apple.com/documentation/vision/vnrecognizetextrequest), [`VNRecognizedText`](https://developer.apple.com/documentation/vision/vnrecognizedtext), and [Vision coordinates](https://developer.apple.com/documentation/vision).

## Completed REST response

`GET /v1/documents/{documentId}` returns the result only for the authenticated owner and only while its Redis TTL is active.

```json
{
  "documentId": "doc_18f673199c0",
  "status": "completed",
  "inputContentType": "application/pdf",
  "inputSizeBytes": 248190,
  "createdAt": "2026-08-15T08:30:00Z",
  "updatedAt": "2026-08-15T08:30:04Z",
  "result": {
    "text": "Invoice 1042\nTotal: $82.00\n\n--- PAGE BREAK ---\n\nThank you",
    "pageCount": 2,
    "pages": [
      {
        "pageNumber": 1,
        "text": "Invoice 1042\nTotal: $82.00",
        "blocks": [
          {
            "text": "Invoice 1042",
            "confidence": 0.9874,
            "bbox": [0.091, 0.862, 0.311, 0.048]
          },
          {
            "text": "Total: $82.00",
            "confidence": 0.9612,
            "bbox": [0.091, 0.781, 0.284, 0.041]
          }
        ]
      },
      {
        "pageNumber": 2,
        "text": "Thank you",
        "blocks": [
          {
            "text": "Thank you",
            "confidence": 0.9941,
            "bbox": [0.411, 0.102, 0.178, 0.039]
          }
        ]
      }
    ]
  },
  "resultExpiresAt": "2026-08-22T08:30:04Z",
  "links": [
    {"rel": "self", "href": "https://ocr.dungxbuif.com/v1/documents/doc_18f673199c0"}
  ]
}
```

## Field semantics

| Field | Meaning and integration rule |
|---|---|
| `result.text` | Plain text for the whole input. For a PDF, non-empty page texts are joined with `\n\n--- PAGE BREAK ---\n\n`. For an image it equals `pages[0].text`. |
| `result.pageCount` | Source page count reported by PDFKit. A supported image has one page. Successful PDF processing returns one `pages` entry for every source page. |
| `result.pages` | Page-level OCR output in source-page order. It may be omitted when a stored result contains no page details, so consumers should tolerate a missing field. |
| `pageNumber` | One-based source page number. Do not use the array index as the business page number. |
| `page.text` | The top recognized candidate from each Vision observation, joined with `\n` in the order returned by Vision. |
| `page.blocks` | Recognized observations for the page. A block is an observation returned by Vision, not a guaranteed word, paragraph, table cell, or semantic line. It may be omitted when empty. |
| `block.text` | The string from `topCandidates(1).first`; alternatives are not exposed. |
| `block.confidence` | Apple's normalized confidence for the selected candidate, from `0` through `1`, where `1` is highest confidence. It is a ranking signal, not a calibrated business probability. |
| `block.bbox` | Four normalized values in this exact order: `[x, y, width, height]`. Vision's origin is the lower-left of the image/page. |
| `resultExpiresAt` | Time at which the Redis-backed OCR payload stops being readable. Persist any business data you need before this timestamp. |

The app preserves the observations and their order as returned by Vision. It does not infer paragraphs, columns, tables, key-value pairs, handwriting fields, or reading order across complex layouts.

## Drawing bounding boxes

All values are relative to page dimensions, so the same response works at any render size. For a canvas with a conventional top-left origin:

```text
left   = x * renderedWidth
top    = (1 - y - height) * renderedHeight
width  = width * renderedWidth
height = height * renderedHeight
```

Example in JavaScript:

```js
function visionBoxToCanvas([x, y, width, height], canvasWidth, canvasHeight) {
  return {
    left: x * canvasWidth,
    top: (1 - y - height) * canvasHeight,
    width: width * canvasWidth,
    height: height * canvasHeight,
  };
}
```

Clamp final pixel values to the canvas bounds before drawing because normalized floating-point values can produce small rounding differences.

## Apple Vision mapping

The worker configures `VNRecognizeTextRequest` and performs it through `VNImageRequestHandler`:

| Public option/output | Apple Vision source |
|---|---|
| `recognitionLevel: "accurate"` | `VNRequestTextRecognitionLevel.accurate`; this is the default. |
| `recognitionLevel: "fast"` | `VNRequestTextRecognitionLevel.fast`. |
| `languages` | `recognitionLanguages`; defaults to `vi-VN` and `en-US`. |
| `automaticallyDetectsLanguage` | Same Vision property on supported macOS versions; defaults to `true`. Explicit `false` is preserved. |
| `usesLanguageCorrection` | Same Vision property; defaults to `true`. Explicit `false` is preserved. |
| `customWords` | `customWords`. |
| `minimumTextHeight` | `minimumTextHeight` when greater than zero. |
| `block.text`, `block.confidence` | First item from `topCandidates(1)`. See Apple's [`VNRecognizedText`](https://developer.apple.com/documentation/vision/vnrecognizedtext) and [`confidence`](https://developer.apple.com/documentation/vision/vnrecognizedtext/confidence). |
| `block.bbox` | `VNRecognizedTextObservation.boundingBox`, expressed in Vision normalized coordinates. |

PDF pages are rendered against a white background at 2× media-box scale before recognition. Standalone images use the first decodable image frame. Consequently, image orientation, render quality, source resolution, compression artifacts, layout complexity, language selection, and the host macOS/Vision version can all affect text and confidence values.

## Empty and failed results

A successful page may contain `text: ""` and no blocks when Vision finds no candidate. That is different from a failed document:

- `queued` or `processing`: no `result`; respect `Retry-After` and poll again.
- `completed`: `result` and `resultExpiresAt` are present while retained.
- `failed`: no `result`; inspect `errorDetail` as a diagnostic message, not a stable machine code.
- `410 RESULT_EXPIRED`: the known document metadata remains, but the Redis result has expired.
- `404 NOT_FOUND`: the ID is unknown, belongs to another account, or its retained database metadata has been deleted.

There is no public document list, delete, cancel, or separate `/result` endpoint. Keep the `documentId` returned at submission and read that exact resource.

## Consumer recommendations

- Persist `result.text` if plain searchable text is sufficient; persist `pages` too if highlights or page-local processing matter.
- Treat `pageNumber` as authoritative and `blocks` as optional.
- Do not reconstruct text by sorting bounding boxes unless your document layout has a tested ordering strategy.
- Use confidence thresholds only after evaluation on your own document set and macOS worker version.
- Store the source/result schema version in your downstream system if reproducibility matters; OCR output may change after operating-system or Vision updates.
