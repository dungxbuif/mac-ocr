# Native OCR evaluation fixture

This evaluation uses `eurotext.png`, the 1024×800 multilingual image embedded by the official Tesseract command-line documentation. The runner downloads the source through GitHub's image cache and pins SHA-256 `e233f7f661e1296c9ad98e23f8679a2a69ce0d3becb8a9aafb679fd5e6a45bd8` so a changed remote file fails closed.

Sources:

- <https://github.com/tesseract-ocr/tesseract/wiki/Command-Line-Usage/ff05e909053c9e2a57c434c2f33fbf8044f2beba>
- <https://camo.githubusercontent.com/4570dd68cedbbc9f91490287be8d0122f9bcdec2600aca7bb739d2d59aa79d4a/687474703a2f2f6465762e626c6f672e666169727761792e6e652e6a702f77702d636f6e74656e742f75706c6f6164732f323031342f30342f6575726f746578742e706e67>

`expected-english.txt` contains the 21-word English portion used for deterministic comparison. The rest of the multilingual page remains useful as diagnostic output but is not part of the pass/fail threshold because installed Apple Vision language availability varies by macOS version.

Run only after manually switching the native menu-bar worker to Online:

```bash
OCR_API_URL=http://localhost:8080 \
OCR_API_KEY=sk_ocr_replace_with_test_key \
python3 scripts/ocr_eval.py
```

The runner verifies proxy and native health, requests a presigned upload, uploads the exact bytes, submits the returned app-owned S3 link, polls the exact document ID, and compares the first English segment. It passes when phrase coverage is at least 75% and word error rate is at most 25%. The complete API result and metrics are written to `.ocr-eval/latest.json`, which is ignored by Git.
