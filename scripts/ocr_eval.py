#!/usr/bin/env python3
"""Run a real presigned-upload OCR evaluation against the local stack."""

from __future__ import annotations

import hashlib
import json
import os
import re
import sys
import tempfile
import time
import unicodedata
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Sequence


ROOT = Path(__file__).resolve().parents[1]
EXPECTED_PATH = ROOT / "testdata" / "ocr-eval" / "expected-english.txt"
REPORT_PATH = ROOT / ".ocr-eval" / "latest.json"
FIXTURE_URL = (
    "https://camo.githubusercontent.com/"
    "4570dd68cedbbc9f91490287be8d0122f9bcdec2600aca7bb739d2d59aa79d4a/"
    "687474703a2f2f6465762e626c6f672e666169727761792e6e652e6a702f"
    "77702d636f6e74656e742f75706c6f6164732f323031342f30342f657572"
    "6f746578742e706e67"
)
FIXTURE_SHA256 = "e233f7f661e1296c9ad98e23f8679a2a69ce0d3becb8a9aafb679fd5e6a45bd8"


def request(
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    body: bytes | None = None,
    timeout: float = 15,
) -> tuple[int, dict[str, str], bytes]:
    req = urllib.request.Request(url, data=body, headers=headers or {}, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return response.status, dict(response.headers.items()), response.read()
    except urllib.error.HTTPError as error:
        payload = error.read()
        detail = payload.decode("utf-8", errors="replace")[:1_000]
        raise RuntimeError(f"{method} {url.split('?')[0]} returned HTTP {error.code}: {detail}") from error


def json_request(
    method: str,
    url: str,
    *,
    api_key: str | None = None,
    payload: dict[str, Any] | None = None,
    timeout: float = 15,
) -> tuple[int, dict[str, str], dict[str, Any]]:
    headers = {"Accept": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    body = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload, separators=(",", ":")).encode()
    status, response_headers, raw = request(method, url, headers=headers, body=body, timeout=timeout)
    return status, response_headers, json.loads(raw)


def normalize(value: str) -> str:
    return re.sub(r"\s+", " ", unicodedata.normalize("NFKC", value).casefold()).strip()


def phrase_normalize(value: str) -> str:
    return re.sub(r"[^\w@.$%#]+", " ", normalize(value), flags=re.UNICODE).strip()


def edit_distance(left: Sequence[str] | str, right: Sequence[str] | str) -> int:
    if len(left) < len(right):
        left, right = right, left
    previous = list(range(len(right) + 1))
    for left_index, left_item in enumerate(left, start=1):
        current = [left_index]
        for right_index, right_item in enumerate(right, start=1):
            current.append(
                min(
                    current[-1] + 1,
                    previous[right_index] + 1,
                    previous[right_index - 1] + (left_item != right_item),
                )
            )
        previous = current
    return previous[-1]


def evaluate(expected: str, actual: str) -> dict[str, Any]:
    expected_normalized = normalize(expected)
    actual_normalized = normalize(actual)
    expected_words = expected_normalized.split()
    actual_prefix_words = actual_normalized.split()[: len(expected_words)]
    actual_prefix = " ".join(actual_prefix_words)
    char_error_rate = edit_distance(expected_normalized, actual_prefix) / max(len(expected_normalized), 1)
    word_error_rate = edit_distance(expected_words, actual_prefix_words) / max(len(expected_words), 1)
    phrases = [phrase_normalize(line) for line in expected.splitlines() if line.strip()]
    searchable = phrase_normalize(actual)
    matched = [phrase for phrase in phrases if phrase in searchable]
    phrase_coverage = len(matched) / max(len(phrases), 1)
    passed = phrase_coverage >= 0.75 and word_error_rate <= 0.25
    return {
        "passed": passed,
        "expectedWords": len(expected_words),
        "comparedWords": len(actual_prefix_words),
        "characterErrorRate": round(char_error_rate, 4),
        "wordErrorRate": round(word_error_rate, 4),
        "phraseCoverage": round(phrase_coverage, 4),
        "matchedPhrases": len(matched),
        "totalPhrases": len(phrases),
        "actualEnglishPrefix": actual_prefix,
    }


def main() -> int:
    api_url = os.environ.get("OCR_API_URL", "http://localhost:8080").rstrip("/")
    native_url = os.environ.get("NATIVE_BASE_URL", "http://localhost:8787").rstrip("/")
    api_key = os.environ.get("OCR_API_KEY", "").strip()
    if not api_key:
        raise RuntimeError("OCR_API_KEY is required; use a disposable test API key")

    try:
        _, _, ready = json_request("GET", f"{api_url}/readyz")
    except Exception as error:
        raise RuntimeError(f"proxy is not reachable at {api_url}") from error
    if ready.get("status") != "ready":
        raise RuntimeError("proxy is not ready")
    try:
        _, _, native_health = json_request("GET", f"{native_url}/health")
    except Exception as error:
        raise RuntimeError(
            "native worker is not Online; switch it on manually in the menu-bar app"
        ) from error
    if native_health.get("status") != "ok":
        raise RuntimeError("native worker is not Online; switch it on manually in the menu-bar app")

    with tempfile.TemporaryDirectory(prefix="macocr-eval-") as temporary_directory:
        fixture_path = Path(temporary_directory) / "eurotext.png"
        _, _, fixture = request("GET", FIXTURE_URL, timeout=30)
        digest = hashlib.sha256(fixture).hexdigest()
        if digest != FIXTURE_SHA256:
            raise RuntimeError(f"fixture checksum changed: expected {FIXTURE_SHA256}, got {digest}")
        fixture_path.write_bytes(fixture)

        _, _, presign = json_request(
            "POST",
            f"{api_url}/v1/uploads/presign",
            api_key=api_key,
            payload={"filename": fixture_path.name, "sizeBytes": len(fixture), "contentType": "image/png"},
        )
        upload_url = presign["uploadUrl"]
        source_url = presign["sourceUrl"]
        upload_headers = {str(key): str(value) for key, value in presign["headers"].items()}
        put_status, _, _ = request("PUT", upload_url, headers=upload_headers, body=fixture, timeout=30)
        if put_status not in (200, 201, 204):
            raise RuntimeError(f"presigned upload returned HTTP {put_status}")

        _, _, submission = json_request(
            "POST",
            f"{api_url}/v1/documents",
            api_key=api_key,
            payload={
                "input": {"url": source_url},
                "options": {
                    "recognitionLevel": "accurate",
                    "languages": ["en-US"],
                    "automaticallyDetectsLanguage": False,
                    "usesLanguageCorrection": True,
                },
            },
        )
        document_id = submission["documentId"]

        deadline = time.monotonic() + float(os.environ.get("OCR_EVAL_TIMEOUT_SECONDS", "120"))
        document: dict[str, Any] | None = None
        while time.monotonic() < deadline:
            _, response_headers, document = json_request(
                "GET", f"{api_url}/v1/documents/{document_id}", api_key=api_key
            )
            state = document.get("status")
            if state == "completed":
                break
            if state in ("failed", "cancelled"):
                raise RuntimeError(f"document {document_id} ended in state {state}: {document.get('error')}")
            delay = min(float(response_headers.get("Retry-After", "1")), 5.0)
            time.sleep(max(delay, 0.25))
        else:
            raise RuntimeError(f"document {document_id} did not complete before timeout")

    assert document is not None
    actual = str(document.get("result", {}).get("text", ""))
    if not actual.strip():
        raise RuntimeError("completed OCR result contains no text")
    expected = EXPECTED_PATH.read_text(encoding="utf-8")
    metrics = evaluate(expected, actual)
    report = {
        "fixture": {"url": FIXTURE_URL, "sha256": FIXTURE_SHA256, "bytes": len(fixture)},
        "documentId": document_id,
        "evaluatedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "metrics": metrics,
        "document": document,
    }
    REPORT_PATH.parent.mkdir(parents=True, exist_ok=True)
    REPORT_PATH.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    print(f"document={document_id}")
    print(f"character_error_rate={metrics['characterErrorRate']:.2%}")
    print(f"word_error_rate={metrics['wordErrorRate']:.2%}")
    print(f"phrase_coverage={metrics['phraseCoverage']:.2%}")
    print(f"report={REPORT_PATH}")
    print("PASS" if metrics["passed"] else "FAIL")
    return 0 if metrics["passed"] else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:  # keep CLI failures concise and secret-safe
        print(f"ERROR: {error}", file=sys.stderr)
        raise SystemExit(1)
