#!/usr/bin/env python3
"""Compare MacOCR, PaddleOCR, and Tesseract on a small shared fixture set."""

from __future__ import annotations

import base64
import collections
import json
import os
import re
import subprocess
import sys
import time
import unicodedata
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageFont
from paddleocr import PaddleOCR


ROOT = Path(__file__).resolve().parents[1]
DATASET = ROOT / "test-reports/production-uat-20260815/dataset"
REPORT_DIR = ROOT / "test-reports/ocr-engine-comparison-20260923"
API_URL = os.environ.get("OCR_API_URL", "https://ocr.dungxbuif.com").rstrip("/")
API_KEY_FILE = Path(os.environ.get("OCR_API_KEY_FILE", "/tmp/macocr_bench_api_key"))


def normalize(value: str) -> str:
    value = unicodedata.normalize("NFKC", value).casefold()
    return re.sub(r"[^\w]+", " ", value, flags=re.UNICODE).strip()


def token_metrics(expected: str, actual: str) -> dict[str, Any]:
    expected_tokens = normalize(expected).split()
    actual_tokens = normalize(actual).split()
    expected_counts = collections.Counter(expected_tokens)
    actual_counts = collections.Counter(actual_tokens)
    matched = sum((expected_counts & actual_counts).values())
    recall = matched / max(len(expected_tokens), 1)
    precision = matched / max(len(actual_tokens), 1)
    return {
        "expectedTokens": len(expected_tokens),
        "actualTokens": len(actual_tokens),
        "matchedTokens": matched,
        "tokenRecall": round(recall, 4),
        "tokenPrecision": round(precision, 4),
    }


def http_json(method: str, url: str, *, api_key: str | None = None, payload: Any | None = None, timeout: float = 60) -> tuple[int, dict[str, str], dict[str, Any]]:
    headers = {"Accept": "application/json"}
    body = None
    if api_key:
        headers["Authorization"] = "Bearer " + api_key
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload, separators=(",", ":")).encode()
    request = urllib.request.Request(url, method=method, headers=headers, data=body)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read()
            return response.status, dict(response.headers.items()), json.loads(raw or b"{}")
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            payload = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            payload = {"raw": raw.decode(errors="replace")[:500]}
        return error.code, dict(error.headers.items()), payload


def make_vietnamese_fixture(path: Path) -> str:
    expected = "\n".join(
        [
            "Hoa don ban hang",
            "Cua hang Ca phe Sai Gon",
            "Ngay 23/09/2026",
            "Tong tien: 185.000 dong",
            "Cam on quy khach va hen gap lai.",
            "Tieng Viet co dau: hoa hong, cong nghe, du lieu.",
        ]
    )
    expected_with_marks = "\n".join(
        [
            "Hóa đơn bán hàng",
            "Cửa hàng Cà phê Sài Gòn",
            "Ngày 23/09/2026",
            "Tổng tiền: 185.000 đồng",
            "Cảm ơn quý khách và hẹn gặp lại.",
            "Tiếng Việt có dấu: hoa hồng, công nghệ, dữ liệu.",
        ]
    )
    font_candidates = [
        "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
        "/System/Library/Fonts/Supplemental/Arial.ttf",
        "/System/Library/Fonts/Supplemental/Times New Roman.ttf",
        "/System/Library/Fonts/Supplemental/Verdana.ttf",
    ]
    font_path = next((item for item in font_candidates if Path(item).exists()), None)
    font = ImageFont.truetype(font_path, 38) if font_path else ImageFont.load_default()
    title_font = ImageFont.truetype(font_path, 48) if font_path else font
    image = Image.new("RGB", (1200, 760), "white")
    draw = ImageDraw.Draw(image)
    y = 60
    for index, line in enumerate(expected_with_marks.splitlines()):
        draw.text((72, y), line, fill="black", font=title_font if index == 0 else font)
        y += 96 if index == 0 else 82
    image.save(path)
    return expected_with_marks


def fixture_set() -> list[dict[str, Any]]:
    REPORT_DIR.mkdir(parents=True, exist_ok=True)
    vi_path = REPORT_DIR / "vietnamese-fixture.png"
    vi_expected = make_vietnamese_fixture(vi_path)
    fixtures = [
        {
            "name": "vietnamese-fixture",
            "path": vi_path,
            "expected": vi_expected,
            "kind": "generated-vietnamese",
        },
        {
            "name": "eurotext",
            "path": DATASET / "eurotext.png",
            "expected": (DATASET / "eurotext.expected.txt").read_text(encoding="utf-8"),
            "kind": "english-text",
        },
        {
            "name": "cord-validation-000",
            "path": DATASET / "cord-validation-000.png",
            "expected": (DATASET / "cord-validation-000.expected.txt").read_text(encoding="utf-8"),
            "kind": "receipt",
        },
        {
            "name": "cord-validation-007",
            "path": DATASET / "cord-validation-007.png",
            "expected": (DATASET / "cord-validation-007.expected.txt").read_text(encoding="utf-8"),
            "kind": "receipt",
        },
    ]
    return fixtures


def extract_paddle_text(result: Any) -> str:
    if isinstance(result, list):
        texts: list[str] = []
        for item in result:
            if isinstance(item, dict):
                texts.extend(str(text) for text in item.get("rec_texts", []) if str(text).strip())
        return "\n".join(texts)
    return str(result)


def run_paddle(name: str, ocr: PaddleOCR, path: Path) -> dict[str, Any]:
    start = time.perf_counter()
    result = ocr.predict(str(path))
    seconds = time.perf_counter() - start
    return {
        "engine": name,
        "seconds": round(seconds, 4),
        "text": extract_paddle_text(result),
    }


def run_tesseract(path: Path) -> dict[str, Any]:
    start = time.perf_counter()
    process = subprocess.run(
        ["tesseract", str(path), "stdout", "-l", "vie+eng", "--oem", "1", "--psm", "6"],
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )
    seconds = time.perf_counter() - start
    if process.returncode != 0:
        raise RuntimeError(process.stderr.strip()[:500])
    return {
        "engine": "tesseract-5.5.3-vie+eng-psm6",
        "seconds": round(seconds, 4),
        "text": process.stdout,
    }


def run_macocr(path: Path, api_key: str) -> dict[str, Any]:
    encoded = base64.b64encode(path.read_bytes()).decode()
    payload = {
        "input": {"base64": encoded},
        "mode": "wait",
        "waitTimeoutMs": 30000,
        "options": {
            "recognitionLevel": "accurate",
            "languages": ["vi-VN", "en-US"],
            "automaticallyDetectsLanguage": True,
            "usesLanguageCorrection": True,
        },
    }
    start = time.perf_counter()
    status, headers, response = http_json("POST", API_URL + "/v1/documents", api_key=api_key, payload=payload, timeout=45)
    if status == 202:
        document_id = response["documentId"]
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            poll_status, poll_headers, response = http_json(
                "GET", API_URL + f"/v1/documents/{document_id}", api_key=api_key, timeout=30
            )
            if poll_status != 200:
                raise RuntimeError(f"poll failed {poll_status}: {response}")
            if response.get("status") == "completed":
                break
            if response.get("status") in {"failed", "cancelled"}:
                raise RuntimeError(f"document ended {response.get('status')}: {response.get('errorDetail')}")
            time.sleep(max(0.25, min(float(poll_headers.get("Retry-After", "1")), 3.0)))
        else:
            raise TimeoutError("MacOCR did not complete before timeout")
    elif status != 200:
        raise RuntimeError(f"submit failed {status}: {response}")
    seconds = time.perf_counter() - start
    return {
        "engine": "macocr-apple-vision-prod",
        "seconds": round(seconds, 4),
        "text": str(response.get("result", {}).get("text", "")),
        "documentId": response.get("documentId"),
    }


def summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    by_engine: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        by_engine.setdefault(row["engine"], []).append(row)
    summary: dict[str, Any] = {}
    for engine, items in by_engine.items():
        summary[engine] = {
            "avgSeconds": round(sum(float(item["seconds"]) for item in items) / len(items), 4),
            "avgRecall": round(sum(float(item["metrics"]["tokenRecall"]) for item in items) / len(items), 4),
            "avgPrecision": round(sum(float(item["metrics"]["tokenPrecision"]) for item in items) / len(items), 4),
        }
    return summary


def main() -> int:
    fixtures = fixture_set()
    api_key = API_KEY_FILE.read_text(encoding="utf-8").strip()
    if not api_key:
        raise RuntimeError(f"missing OCR API key file: {API_KEY_FILE}")

    versions = {
        "python": sys.version.split()[0],
        "paddle": __import__("paddle").__version__,
        "paddleocr": __import__("paddleocr").__version__,
        "tesseract": subprocess.run(["tesseract", "--version"], capture_output=True, text=True, check=False).stdout.splitlines()[0],
    }

    paddle_default = PaddleOCR(lang="vi")
    paddle_fast = PaddleOCR(
        lang="vi",
        use_doc_orientation_classify=False,
        use_doc_unwarping=False,
        use_textline_orientation=False,
        text_det_limit_side_len=960,
    )

    rows: list[dict[str, Any]] = []
    for fixture in fixtures:
        engines = [
            run_paddle("paddleocr-3.7.0-vi-default", paddle_default, fixture["path"]),
            run_paddle("paddleocr-3.7.0-vi-fast-no-orient-unwarp", paddle_fast, fixture["path"]),
            run_tesseract(fixture["path"]),
            run_macocr(fixture["path"], api_key),
        ]
        for result in engines:
            row = {
                "fixture": fixture["name"],
                "kind": fixture["kind"],
                "engine": result["engine"],
                "seconds": result["seconds"],
                "metrics": token_metrics(fixture["expected"], result["text"]),
                "textPreview": normalize(result["text"])[:300],
            }
            if "documentId" in result:
                row["documentId"] = result["documentId"]
            rows.append(row)
            print(
                f"{fixture['name']} | {row['engine']} | {row['seconds']:.2f}s | "
                f"recall={row['metrics']['tokenRecall']:.1%} precision={row['metrics']['tokenPrecision']:.1%}",
                flush=True,
            )

    report = {
        "createdAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "versions": versions,
        "fixtures": [
            {"name": item["name"], "kind": item["kind"], "path": str(item["path"])} for item in fixtures
        ],
        "summary": summarize(rows),
        "rows": rows,
    }
    REPORT_DIR.mkdir(parents=True, exist_ok=True)
    (REPORT_DIR / "summary.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    lines = ["# OCR engine comparison", "", "## Summary", ""]
    lines.append("| Engine | Avg seconds | Avg token recall | Avg token precision |")
    lines.append("|---|---:|---:|---:|")
    for engine, item in sorted(report["summary"].items()):
        lines.append(
            f"| `{engine}` | {item['avgSeconds']:.2f}s | {item['avgRecall']:.1%} | {item['avgPrecision']:.1%} |"
        )
    lines.extend(["", "## Per fixture", "", "| Fixture | Engine | Seconds | Recall | Precision |", "|---|---|---:|---:|---:|"])
    for row in rows:
        lines.append(
            f"| `{row['fixture']}` | `{row['engine']}` | {row['seconds']:.2f}s | "
            f"{row['metrics']['tokenRecall']:.1%} | {row['metrics']['tokenPrecision']:.1%} |"
        )
    (REPORT_DIR / "README.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"REPORT {REPORT_DIR}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
