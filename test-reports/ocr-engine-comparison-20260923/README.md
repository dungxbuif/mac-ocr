# OCR engine comparison

## Summary

| Engine | Avg seconds | Avg token recall | Avg token precision |
|---|---:|---:|---:|
| `macocr-apple-vision-prod` | 1.30s | 97.6% | 80.3% |
| `paddleocr-3.7.0-vi-default` | 3.68s | 87.5% | 68.9% |
| `paddleocr-3.7.0-vi-fast-no-orient-unwarp` | 4.08s | 87.5% | 71.7% |
| `tesseract-5.5.3-vie+eng-psm6` | 0.37s | 62.6% | 58.1% |

## Per fixture

| Fixture | Engine | Seconds | Recall | Precision |
|---|---|---:|---:|---:|
| `vietnamese-fixture` | `paddleocr-3.7.0-vi-default` | 3.43s | 56.8% | 56.8% |
| `vietnamese-fixture` | `paddleocr-3.7.0-vi-fast-no-orient-unwarp` | 4.04s | 56.8% | 56.8% |
| `vietnamese-fixture` | `tesseract-5.5.3-vie+eng-psm6` | 0.32s | 100.0% | 100.0% |
| `vietnamese-fixture` | `macocr-apple-vision-prod` | 1.15s | 97.3% | 97.3% |
| `eurotext` | `paddleocr-3.7.0-vi-default` | 4.39s | 100.0% | 37.0% |
| `eurotext` | `paddleocr-3.7.0-vi-fast-no-orient-unwarp` | 4.60s | 100.0% | 37.0% |
| `eurotext` | `tesseract-5.5.3-vie+eng-psm6` | 0.41s | 100.0% | 37.0% |
| `eurotext` | `macocr-apple-vision-prod` | 1.50s | 100.0% | 37.0% |
| `cord-validation-000` | `paddleocr-3.7.0-vi-default` | 3.51s | 100.0% | 100.0% |
| `cord-validation-000` | `paddleocr-3.7.0-vi-fast-no-orient-unwarp` | 3.59s | 100.0% | 100.0% |
| `cord-validation-000` | `tesseract-5.5.3-vie+eng-psm6` | 0.38s | 33.3% | 50.0% |
| `cord-validation-000` | `macocr-apple-vision-prod` | 1.45s | 100.0% | 100.0% |
| `cord-validation-007` | `paddleocr-3.7.0-vi-default` | 3.39s | 93.1% | 81.8% |
| `cord-validation-007` | `paddleocr-3.7.0-vi-fast-no-orient-unwarp` | 4.10s | 93.1% | 93.1% |
| `cord-validation-007` | `tesseract-5.5.3-vie+eng-psm6` | 0.35s | 17.2% | 45.5% |
| `cord-validation-007` | `macocr-apple-vision-prod` | 1.10s | 93.1% | 87.1% |
