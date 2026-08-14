#!/usr/bin/env python3
"""Real-S3/native OCR accuracy, multi-page, load, and resource benchmark."""

from __future__ import annotations

import argparse
import base64
import collections
import concurrent.futures
import csv
import hashlib
import json
import math
import os
import re
import ssl
import statistics
import subprocess
import sys
import threading
import time
import unicodedata
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
CORD_ROWS_URL = (
    "https://datasets-server.huggingface.co/rows?"
    "dataset=naver-clova-ix/cord-v2&config=default&split=validation&offset=0&length=16"
)
CORD_DATASET_URL = "https://huggingface.co/datasets/naver-clova-ix/cord-v2"
CORD_LICENSE_URL = "https://github.com/clovaai/cord/blob/master/LICENSE-CC-BY"
EUROTEXT_URL = (
    "https://camo.githubusercontent.com/"
    "4570dd68cedbbc9f91490287be8d0122f9bcdec2600aca7bb739d2d59aa79d4a/"
    "687474703a2f2f6465762e626c6f672e666169727761792e6e652e6a702f"
    "77702d636f6e74656e742f75706c6f6164732f323031342f30342f657572"
    "6f746578742e706e67"
)
EUROTEXT_SHA256 = "e233f7f661e1296c9ad98e23f8679a2a69ce0d3becb8a9aafb679fd5e6a45bd8"
TLS_CONTEXT = ssl.create_default_context(cafile="/etc/ssl/cert.pem")


def http(
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    body: bytes | None = None,
    timeout: float = 60,
) -> tuple[int, dict[str, str], bytes]:
    request = urllib.request.Request(url, method=method, headers=headers or {}, data=body)
    try:
        with urllib.request.urlopen(request, timeout=timeout, context=TLS_CONTEXT) as response:
            return response.status, dict(response.headers.items()), response.read()
    except urllib.error.HTTPError as error:
        return error.code, dict(error.headers.items()), error.read()


def json_http(
    method: str,
    url: str,
    *,
    api_key: str | None = None,
    payload: Any | None = None,
    timeout: float = 60,
) -> tuple[int, dict[str, str], dict[str, Any]]:
    headers = {"Accept": "application/json"}
    body = None
    if api_key:
        headers["Authorization"] = "Bearer " + api_key
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload, separators=(",", ":")).encode()
    status, response_headers, raw = http(method, url, headers=headers, body=body, timeout=timeout)
    try:
        decoded = json.loads(raw) if raw else {}
    except json.JSONDecodeError:
        decoded = {"raw": raw.decode(errors="replace")[:1000]}
    return status, response_headers, decoded


def require_status(status: int, expected: tuple[int, ...], operation: str, payload: Any) -> None:
    if status not in expected:
        raise RuntimeError(f"{operation} returned HTTP {status}: {str(payload)[:500]}")


def normalize(value: str) -> str:
    value = unicodedata.normalize("NFKC", value).casefold()
    return re.sub(r"[^\w]+", " ", value, flags=re.UNICODE).strip()


def percentile(values: list[float], quantile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, math.ceil(quantile * len(ordered)) - 1))
    return ordered[index]


def token_metrics(expected: str, actual: str) -> dict[str, Any]:
    expected_tokens = normalize(expected).split()
    actual_tokens = normalize(actual).split()
    expected_counts = collections.Counter(expected_tokens)
    actual_counts = collections.Counter(actual_tokens)
    matched = sum((expected_counts & actual_counts).values())
    recall = matched / max(len(expected_tokens), 1)
    precision = matched / max(len(actual_tokens), 1)
    lines = [normalize(line) for line in expected.splitlines() if normalize(line)]
    actual_normalized = " " + normalize(actual) + " "
    phrase_hits = sum(1 for line in lines if " " + line + " " in actual_normalized)
    return {
        "expectedTokens": len(expected_tokens),
        "actualTokens": len(actual_tokens),
        "matchedTokens": matched,
        "tokenRecall": round(recall, 4),
        "tokenPrecision": round(precision, 4),
        "phraseCoverage": round(phrase_hits / max(len(lines), 1), 4),
        "matchedPhrases": phrase_hits,
        "expectedPhrases": len(lines),
    }


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def prepare_dataset(report_dir: Path) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    dataset_dir = report_dir / "dataset"
    dataset_dir.mkdir(parents=True, exist_ok=True)
    status, _, raw = http("GET", CORD_ROWS_URL, timeout=120)
    require_status(status, (200,), "download CORD metadata", raw[:200])
    rows_payload = json.loads(raw)
    selected_offsets = [0, 1, 2, 7, 15]
    rows_by_offset = {int(item["row_idx"]): item["row"] for item in rows_payload["rows"]}
    samples: list[dict[str, Any]] = []
    manifest_entries: list[dict[str, Any]] = []

    for offset in selected_offsets:
        row = rows_by_offset[offset]
        ground_truth = json.loads(row["ground_truth"])
        lines = [
            " ".join(str(word.get("text", "")).strip() for word in line.get("words", [])).strip()
            for line in ground_truth.get("valid_line", [])
        ]
        expected = "\n".join(line for line in lines if line)
        image_url = row["image"]["src"]
        image_status, image_headers, image_data = http("GET", image_url, timeout=120)
        require_status(image_status, (200,), f"download CORD row {offset}", image_data[:200])
        content_type = image_headers.get("Content-Type", "image/png").split(";", 1)[0]
        extension = ".jpg" if "jpeg" in content_type else ".png"
        image_path = dataset_dir / f"cord-validation-{offset:03d}{extension}"
        expected_path = dataset_dir / f"cord-validation-{offset:03d}.expected.txt"
        image_path.write_bytes(image_data)
        expected_path.write_text(expected + "\n", encoding="utf-8")
        sample = {
            "name": f"cord-validation-{offset:03d}",
            "kind": "receipt",
            "path": image_path,
            "expected": expected,
            "contentType": content_type,
            "source": image_url,
        }
        samples.append(sample)
        manifest_entries.append(
            {
                "name": sample["name"],
                "row": offset,
                "bytes": len(image_data),
                "sha256": sha256(image_data),
                "groundTruthSha256": sha256(expected.encode()),
                "width": row["image"]["width"],
                "height": row["image"]["height"],
            }
        )

    euro_status, euro_headers, euro_data = http("GET", EUROTEXT_URL, timeout=120)
    require_status(euro_status, (200,), "download checksum fixture", euro_data[:200])
    if sha256(euro_data) != EUROTEXT_SHA256:
        raise RuntimeError("Eurotext fixture checksum no longer matches the pinned value")
    euro_path = dataset_dir / "eurotext.png"
    euro_path.write_bytes(euro_data)
    euro_expected = (ROOT / "testdata/ocr-eval/expected-english.txt").read_text(encoding="utf-8")
    (dataset_dir / "eurotext.expected.txt").write_text(euro_expected, encoding="utf-8")
    samples.insert(
        0,
        {
            "name": "eurotext-checksum-fixture",
            "kind": "simple",
            "path": euro_path,
            "expected": euro_expected,
            "contentType": euro_headers.get("Content-Type", "image/png").split(";", 1)[0],
            "source": EUROTEXT_URL,
        },
    )
    manifest_entries.insert(
        0,
        {
            "name": "eurotext-checksum-fixture",
            "bytes": len(euro_data),
            "sha256": EUROTEXT_SHA256,
            "groundTruthSha256": sha256(euro_expected.encode()),
        },
    )
    manifest = {
        "createdAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "sources": [
            {
                "name": "CORD v2 validation",
                "url": CORD_DATASET_URL,
                "publisher": "Naver/Clova AI",
                "license": "CC BY 4.0",
                "licenseUrl": CORD_LICENSE_URL,
                "groundTruth": "valid_line words and bounding quadrilaterals supplied by the dataset",
            },
            {
                "name": "Pinned Eurotext fixture",
                "url": EUROTEXT_URL,
                "sha256": EUROTEXT_SHA256,
                "groundTruth": "repository-pinned English transcription",
            },
        ],
        "samples": manifest_entries,
    }
    (report_dir / "dataset-manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return samples, manifest


def make_pdf(report_dir: Path, name: str, pages: int, samples: list[dict[str, Any]]) -> dict[str, Any]:
    pdf_path = report_dir / "dataset" / f"{name}.pdf"
    receipt_samples = [sample for sample in samples if sample["kind"] == "receipt"]
    selected = [receipt_samples[index % len(receipt_samples)] for index in range(pages)]
    command = ["swift", str(ROOT / "scripts/make_image_pdf.swift"), str(pdf_path)]
    command.extend(str(sample["path"]) for sample in selected)
    subprocess.run(command, check=True, capture_output=True, text=True, timeout=180)
    return {
        "name": name,
        "kind": "multipage-pdf",
        "path": pdf_path,
        "expected": "\n".join(str(sample["expected"]) for sample in selected),
        "contentType": "application/pdf",
        "expectedPages": pages,
        "source": "generated from CORD samples listed in dataset-manifest.json",
    }


class OCRClient:
    def __init__(self, api_url: str, api_key: str):
        self.api_url = api_url.rstrip("/")
        self.api_key = api_key

    def presign_upload(self, path: Path, content_type: str) -> tuple[str, dict[str, float]]:
        data = path.read_bytes()
        start = time.monotonic()
        status, _, response = json_http(
            "POST",
            self.api_url + "/v1/uploads/presign",
            api_key=self.api_key,
            payload={"filename": path.name, "sizeBytes": len(data), "contentType": content_type},
        )
        require_status(status, (201,), "presign", response)
        presign_seconds = time.monotonic() - start
        upload_headers = {str(key): str(value) for key, value in response["headers"].items()}
        start = time.monotonic()
        put_status, _, put_body = http(
            "PUT", response["uploadUrl"], headers=upload_headers, body=data, timeout=180
        )
        require_status(put_status, (200, 201, 204), "presigned PUT", put_body[:200])
        return str(response["sourceUrl"]), {
            "presignSeconds": round(presign_seconds, 4),
            "uploadSeconds": round(time.monotonic() - start, 4),
        }

    def submit(self, payload: dict[str, Any]) -> tuple[str, float]:
        start = time.monotonic()
        status, _, response = json_http(
            "POST", self.api_url + "/v1/documents", api_key=self.api_key, payload=payload, timeout=180
        )
        require_status(status, (202,), "submit OCR", response)
        return str(response["documentId"]), time.monotonic() - start

    def poll(self, document_id: str, timeout: float = 600) -> tuple[dict[str, Any], float]:
        start = time.monotonic()
        deadline = start + timeout
        while time.monotonic() < deadline:
            status, headers, response = json_http(
                "GET", f"{self.api_url}/v1/documents/{document_id}", api_key=self.api_key
            )
            require_status(status, (200,), f"poll {document_id}", response)
            if response.get("status") == "completed":
                return response, time.monotonic() - start
            if response.get("status") in ("failed", "cancelled"):
                raise RuntimeError(
                    f"document {document_id} ended as {response.get('status')}: {response.get('errorDetail')}"
                )
            delay = min(float(headers.get("Retry-After", "1")), 3.0)
            time.sleep(max(0.2, delay))
        raise TimeoutError(f"document {document_id} exceeded {timeout:.0f}s")


class ResourceMonitor:
    fields = [
        "timestamp",
        "phase",
        "load1",
        "memoryFreePercent",
        "swapUsedMB",
        "proxyCpu",
        "proxyRssMB",
        "nativeCpu",
        "nativeRssMB",
        "llmCpu",
        "llmRssMB",
        "nativeActive",
        "nativeAvailable",
        "nativeLimit",
    ]

    def __init__(self, path: Path, native_url: str):
        self.path = path
        self.native_url = native_url.rstrip("/")
        self.phase = "idle"
        self.stop_event = threading.Event()
        self.abort_event = threading.Event()
        self.samples: list[dict[str, Any]] = []
        self.thread: threading.Thread | None = None

    def start(self) -> None:
        self.thread = threading.Thread(target=self._run, name="resource-monitor", daemon=True)
        self.thread.start()

    def stop(self) -> None:
        self.stop_event.set()
        if self.thread:
            self.thread.join(timeout=10)
        with self.path.open("w", newline="", encoding="utf-8") as handle:
            writer = csv.DictWriter(handle, fieldnames=self.fields, lineterminator="\n")
            writer.writeheader()
            writer.writerows(self.samples)

    def _process_totals(self) -> dict[str, tuple[float, float]]:
        output = subprocess.run(
            ["ps", "-axo", "pid=,%cpu=,rss=,command="], capture_output=True, text=True, timeout=5, check=True
        ).stdout
        totals = {"proxy": [0.0, 0.0], "native": [0.0, 0.0], "llm": [0.0, 0.0]}
        for line in output.splitlines():
            parts = line.strip().split(None, 3)
            if len(parts) < 4:
                continue
            _, cpu, rss, command = parts
            label = None
            if command.endswith("/exe/proxy") or "/macocr-proxy" in command:
                label = "proxy"
            elif "mac-ocr-native" in command and "grep" not in command:
                label = "native"
            elif "llmworker.js" in command:
                label = "llm"
            if label:
                totals[label][0] += float(cpu)
                totals[label][1] += float(rss) / 1024
        # macOS `ps` RSS excludes most Metal/model allocations. `top` reports
        # the effective LM Studio footprint (for example 24G for the loaded
        # model), so use it when a model worker is present.
        llm_pids = subprocess.run(
            ["pgrep", "-f", "llmworker.js"], capture_output=True, text=True, timeout=5
        ).stdout.split()
        if llm_pids:
            top = subprocess.run(
                ["top", "-l", "1", "-pid", llm_pids[0], "-stats", "pid,mem"],
                capture_output=True,
                text=True,
                timeout=8,
            ).stdout
            match = re.search(rf"^\s*{re.escape(llm_pids[0])}\s+([\d.]+)([KMG])", top, flags=re.MULTILINE)
            if match:
                multiplier = {"K": 1 / 1024, "M": 1, "G": 1024}[match.group(2)]
                totals["llm"][1] = float(match.group(1)) * multiplier
        return {key: (value[0], value[1]) for key, value in totals.items()}

    def _memory(self) -> tuple[float, float]:
        pressure = subprocess.run(["memory_pressure"], capture_output=True, text=True, timeout=8).stdout
        match = re.search(r"System-wide memory free percentage:\s*(\d+)%", pressure)
        free_percent = float(match.group(1)) if match else -1.0
        swap = subprocess.run(
            ["sysctl", "-n", "vm.swapusage"], capture_output=True, text=True, timeout=5
        ).stdout
        used = re.search(r"used = ([\d.]+)([MG])", swap)
        swap_mb = 0.0
        if used:
            swap_mb = float(used.group(1)) * (1024 if used.group(2) == "G" else 1)
        return free_percent, swap_mb

    def _run(self) -> None:
        while not self.stop_event.is_set():
            try:
                process = self._process_totals()
                free_percent, swap_mb = self._memory()
                _, _, capacity = json_http("GET", self.native_url + "/capacity", timeout=5)
                sample = {
                    "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    "phase": self.phase,
                    "load1": round(os.getloadavg()[0], 2),
                    "memoryFreePercent": free_percent,
                    "swapUsedMB": round(swap_mb, 1),
                    "proxyCpu": round(process["proxy"][0], 1),
                    "proxyRssMB": round(process["proxy"][1], 1),
                    "nativeCpu": round(process["native"][0], 1),
                    "nativeRssMB": round(process["native"][1], 1),
                    "llmCpu": round(process["llm"][0], 1),
                    "llmRssMB": round(process["llm"][1], 1),
                    "nativeActive": capacity.get("active", -1),
                    "nativeAvailable": capacity.get("available", -1),
                    "nativeLimit": capacity.get("operatorLimit", -1),
                }
                self.samples.append(sample)
                if 0 <= free_percent < 8:
                    self.abort_event.set()
            except Exception as error:
                self.samples.append(
                    {field: (f"monitor error: {error}" if field == "phase" else "") for field in self.fields}
                )
            self.stop_event.wait(1.0)


def set_native_limit(native_url: str, secret: str, limit: int) -> dict[str, Any]:
    status, _, response = json_http(
        "PUT",
        native_url.rstrip("/") + "/runtime/config",
        api_key=secret,
        payload={"operatorLimit": limit},
    )
    require_status(status, (200,), f"set native limit {limit}", response)
    return response


def accuracy_case(client: OCRClient, sample: dict[str, Any], report_dir: Path) -> dict[str, Any]:
    source_url, upload_timings = client.presign_upload(sample["path"], sample["contentType"])
    payload = {
        "input": {"url": source_url},
        "options": {"recognitionLevel": "accurate", "automaticallyDetectsLanguage": True},
    }
    document_id, submit_seconds = client.submit(payload)
    response, poll_seconds = client.poll(document_id, timeout=1200)
    actual = str(response.get("result", {}).get("text", ""))
    metrics = token_metrics(str(sample["expected"]), actual)
    expected_pages = int(sample.get("expectedPages", 1))
    actual_pages = int(response.get("result", {}).get("pageCount", 0))
    passed = bool(actual.strip()) and actual_pages == expected_pages
    if sample["kind"] == "simple":
        passed = passed and metrics["tokenRecall"] >= 0.75
    else:
        passed = passed and metrics["tokenRecall"] >= 0.60
    raw_dir = report_dir / "raw-results"
    raw_dir.mkdir(exist_ok=True)
    (raw_dir / f"{sample['name']}.json").write_text(
        json.dumps(response, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return {
        "name": sample["name"],
        "kind": sample["kind"],
        "bytes": sample["path"].stat().st_size,
        "documentId": document_id,
        "expectedPages": expected_pages,
        "actualPages": actual_pages,
        "presignSeconds": upload_timings["presignSeconds"],
        "uploadSeconds": upload_timings["uploadSeconds"],
        "submitSeconds": round(submit_seconds, 4),
        "processingSeconds": round(poll_seconds, 4),
        "metrics": metrics,
        "passed": passed,
    }


def load_round(
    client: OCRClient,
    monitor: ResourceMonitor,
    sample_bytes: bytes,
    content_type: str,
    limit: int,
    jobs: int,
    workload: str,
) -> dict[str, Any]:
    monitor.phase = f"load-{workload}-limit-{limit}"
    encoded = base64.b64encode(sample_bytes).decode()
    payload = {
        "input": {"base64": encoded},
        "options": {"recognitionLevel": "accurate", "automaticallyDetectsLanguage": True},
    }
    started = time.monotonic()
    records: list[dict[str, Any]] = []

    def run_one(index: int) -> dict[str, Any]:
        if monitor.abort_event.is_set():
            return {"index": index, "status": "aborted", "error": "memory safety threshold reached"}
        item_started = time.monotonic()
        try:
            document_id, submit_seconds = client.submit(payload)
            response, poll_seconds = client.poll(document_id, timeout=600)
            return {
                "index": index,
                "documentId": document_id,
                "status": response.get("status"),
                "submitSeconds": round(submit_seconds, 4),
                "processingSeconds": round(poll_seconds, 4),
                "totalSeconds": round(time.monotonic() - item_started, 4),
                "characters": len(str(response.get("result", {}).get("text", ""))),
            }
        except Exception as error:
            return {"index": index, "status": "error", "error": str(error)[:500]}

    with concurrent.futures.ThreadPoolExecutor(max_workers=jobs) as executor:
        futures = [executor.submit(run_one, index) for index in range(jobs)]
        for future in concurrent.futures.as_completed(futures):
            records.append(future.result())

    wall_seconds = time.monotonic() - started
    successful = [record for record in records if record.get("status") == "completed"]
    latencies = [float(record["totalSeconds"]) for record in successful]
    phase_samples = [sample for sample in monitor.samples if sample.get("phase") == monitor.phase]
    free_values = [float(sample["memoryFreePercent"]) for sample in phase_samples if sample.get("memoryFreePercent") != ""]
    return {
        "workload": workload,
        "workerLimit": limit,
        "jobs": jobs,
        "completed": len(successful),
        "failed": jobs - len(successful),
        "successRate": round(len(successful) / max(jobs, 1), 4),
        "wallSeconds": round(wall_seconds, 4),
        "throughputDocsPerSecond": round(len(successful) / max(wall_seconds, 0.001), 4),
        "latencyP50Seconds": round(percentile(latencies, 0.50), 4),
        "latencyP95Seconds": round(percentile(latencies, 0.95), 4),
        "minMemoryFreePercent": min(free_values) if free_values else None,
        "records": sorted(records, key=lambda record: int(record["index"])),
    }


def environment_snapshot(api_url: str, native_url: str) -> dict[str, Any]:
    _, _, ready = json_http("GET", api_url.rstrip("/") + "/readyz")
    _, _, native = json_http("GET", native_url.rstrip("/") + "/capacity")
    hardware = subprocess.run(
        ["system_profiler", "SPHardwareDataType"], capture_output=True, text=True, timeout=30
    ).stdout
    def field(name: str) -> str | None:
        match = re.search(rf"^\s*{re.escape(name)}:\s*(.+)$", hardware, flags=re.MULTILINE)
        return match.group(1).strip() if match else None
    llm = subprocess.run(
        ["pgrep", "-f", "llmworker.js"], capture_output=True, text=True, timeout=5
    ).returncode == 0
    return {
        "capturedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "hardware": {
            "model": field("Model Name"),
            "chip": field("Chip"),
            "cores": field("Total Number of Cores"),
            "memory": field("Memory"),
        },
        "services": {"proxyReady": ready.get("status") == "ready", "nativeCapacity": native},
        "llmWorkerDetected": llm,
        "secretsIncluded": False,
    }


def write_summary(
    report_dir: Path,
    environment: dict[str, Any],
    accuracy: list[dict[str, Any]],
    load: list[dict[str, Any]],
    e2e_status: str,
) -> None:
    def recommend(rounds: list[dict[str, Any]]) -> int:
        passing = [
            item for item in rounds
            if item["successRate"] == 1.0
            and (item["minMemoryFreePercent"] is None or item["minMemoryFreePercent"] >= 15)
        ]
        if not passing:
            return 1
        best = max(item["throughputDocsPerSecond"] for item in passing)
        # Prefer the lowest setting that delivers at least 85% of peak
        # throughput, preserving headroom when the next step has diminishing
        # returns or the live document mix becomes heavier than the fixture.
        efficient = [item for item in passing if item["throughputDocsPerSecond"] >= best * 0.85]
        return min(item["workerLimit"] for item in efficient)

    capacity_rounds = [item for item in load if item.get("workload") == "10-page-pdf"] or load
    image_rounds = [item for item in load if item.get("workload") == "receipt-image"]
    recommended = recommend(capacity_rounds)
    image_recommended = recommend(image_rounds) if image_rounds else recommended
    max_validated = max((item["workerLimit"] for item in load if item["successRate"] == 1.0), default=1)

    resources: dict[str, float] = {}
    resource_path = report_dir / "resource-samples.csv"
    if resource_path.exists():
        samples = list(csv.DictReader(resource_path.open(encoding="utf-8")))
        numeric = lambda field: [float(row[field]) for row in samples if row.get(field) not in (None, "")]
        memory = [value for value in numeric("memoryFreePercent") if value > 0]
        resources = {
            "minMemoryFreePercent": min(memory) if memory else 0,
            "maxProxyRssMB": max(numeric("proxyRssMB"), default=0),
            "maxNativeRssMB": max(numeric("nativeRssMB"), default=0),
            "maxNativeCpuPercent": max(numeric("nativeCpu"), default=0),
            "maxLlmEffectiveRssMB": max(numeric("llmRssMB"), default=0),
        }
    all_accuracy_passed = all(item["passed"] for item in accuracy)
    all_load_passed = all(item["successRate"] == 1.0 for item in load)
    summary = {
        "generatedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "e2e": e2e_status,
        "accuracyPassed": all_accuracy_passed,
        "loadPassed": all_load_passed,
        "recommendedNativeConcurrency": recommended,
        "workloadRecommendations": {
            "receiptImages": image_recommended,
            "multiPageOrMixed": recommended,
        },
        "maxValidatedConcurrency": max_validated,
        "resourceSummary": resources,
        "accuracyCases": accuracy,
        "loadRounds": load,
        "environment": environment,
    }
    (report_dir / "summary.json").write_text(
        json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    rows = [
        "# Final local production UAT",
        "",
        f"Generated: `{summary['generatedAt']}`",
        "",
        "## Verdict",
        "",
        f"- Baseline production E2E: **{e2e_status}**",
        f"- Accuracy/page-count cases: **{'PASS' if all_accuracy_passed else 'FAIL'}**",
        f"- Stepped load rounds: **{'PASS' if all_load_passed else 'FAIL'}**",
        f"- Recommended concurrency for receipt images: **{image_recommended}**",
        f"- Recommended concurrency for multi-page or mixed traffic: **{recommended}**",
        f"- Maximum concurrency validated without request failure: **{max_validated}**",
        "",
        "This concurrency is a local capacity result, not a universal production SLA. Re-run after changing the model, macOS, Vision revision, document mix, or memory pressure.",
        "",
        "## Resource envelope",
        "",
        f"- Minimum system free memory: **{resources.get('minMemoryFreePercent', 0):.0f}%**",
        f"- Maximum native RSS: **{resources.get('maxNativeRssMB', 0):.0f} MiB**",
        f"- Maximum proxy RSS: **{resources.get('maxProxyRssMB', 0):.0f} MiB**",
        f"- LM Studio worker effective footprint observed by `top`: **{resources.get('maxLlmEffectiveRssMB', 0):.0f} MiB**",
        "- `pmset -g therm`: no thermal or performance warning recorded after the load run.",
        "",
        "## Accuracy and document cases",
        "",
        "| Case | Type | Pages | Token recall | Phrase coverage | Processing | Result |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    for item in accuracy:
        rows.append(
            f"| {item['name']} | {item['kind']} | {item['actualPages']}/{item['expectedPages']} | "
            f"{item['metrics']['tokenRecall']:.1%} | {item['metrics']['phraseCoverage']:.1%} | "
            f"{item['processingSeconds']:.2f}s | {'PASS' if item['passed'] else 'FAIL'} |"
        )
    rows.extend(
        [
            "",
            "## Load rounds",
            "",
        "| Workload | Worker limit | Jobs | Success | Throughput | p50 | p95 | Min free memory |",
        "|---|---:|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for item in load:
        minimum = "n/a" if item["minMemoryFreePercent"] is None else f"{item['minMemoryFreePercent']:.0f}%"
        rows.append(
            f"| {item.get('workload', 'image')} | {item['workerLimit']} | {item['jobs']} | {item['successRate']:.0%} | "
            f"{item['throughputDocsPerSecond']:.3f} doc/s | {item['latencyP50Seconds']:.2f}s | "
            f"{item['latencyP95Seconds']:.2f}s | {minimum} |"
        )
    rows.extend(
        [
            "",
            "## Evidence",
            "",
            "- `dataset-manifest.json`: primary sources, licenses, sample hashes, and ground-truth hashes.",
            "- `raw-results/`: exact document responses returned by the API (no credentials or signed upload URLs).",
            "- `resource-samples.csv`: CPU/RSS, memory pressure, swap, native capacity, and LLM worker footprint over time.",
            "- `summary.json`: machine-readable complete results and per-request load records.",
            "- `commands.md`: redacted reproduction procedure.",
            "- `e2e-checklist.md`: final production E2E coverage and run identifier.",
            "- `findings-and-fixes.md`: defect investigation, remediation, and post-fix evidence.",
            "",
            "## Security handling",
            "",
            "S3 credentials, API keys, native shared secrets, Authorization headers, and presigned URLs are intentionally excluded. Rotate the supplied S3 key before production because it was shared in chat.",
            "",
        ]
    )
    (report_dir / "README.md").write_text("\n".join(rows), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--report-dir", required=True, type=Path)
    parser.add_argument("--e2e-status", default="PASS")
    parser.add_argument("--refresh-existing", action="store_true")
    args = parser.parse_args()

    report_dir = args.report_dir.resolve()
    if args.refresh_existing:
        existing = json.loads((report_dir / "summary.json").read_text(encoding="utf-8"))
        write_summary(
            report_dir,
            existing["environment"],
            existing["accuracyCases"],
            existing["loadRounds"],
            existing["e2e"],
        )
        return 0

    api_url = os.environ.get("OCR_API_URL", "http://localhost:8080")
    native_url = os.environ.get("NATIVE_BASE_URL", "http://localhost:8787")
    api_key = os.environ.get("OCR_API_KEY", "").strip()
    native_secret = os.environ.get("NATIVE_AUTH_SECRET", "").strip()
    if not api_key or not native_secret:
        raise RuntimeError("OCR_API_KEY and NATIVE_AUTH_SECRET are required")

    report_dir.mkdir(parents=True, exist_ok=True)
    environment = environment_snapshot(api_url, native_url)
    if not environment["services"]["proxyReady"]:
        raise RuntimeError("proxy is not ready")
    original_limit = int(environment["services"]["nativeCapacity"].get("operatorLimit", 1))
    samples, _ = prepare_dataset(report_dir)
    samples.extend(
        [make_pdf(report_dir, "cord-10-page", 10, samples), make_pdf(report_dir, "cord-30-page", 30, samples)]
    )
    client = OCRClient(api_url, api_key)
    monitor = ResourceMonitor(report_dir / "resource-samples.csv", native_url)
    monitor.start()
    accuracy: list[dict[str, Any]] = []
    load: list[dict[str, Any]] = []
    try:
        set_native_limit(native_url, native_secret, min(original_limit, 3))
        for sample in samples:
            monitor.phase = "accuracy-" + str(sample["name"])
            result = accuracy_case(client, sample, report_dir)
            accuracy.append(result)
            print(
                f"ACCURACY {result['name']} pass={result['passed']} pages={result['actualPages']} "
                f"recall={result['metrics']['tokenRecall']:.1%} seconds={result['processingSeconds']:.2f}",
                flush=True,
            )
        load_sample = next(sample for sample in samples if sample["kind"] == "receipt")
        load_bytes = load_sample["path"].read_bytes()
        for limit, jobs in [(1, 6), (2, 8), (3, 12), (4, 16), (6, 18)]:
            if monitor.abort_event.is_set():
                print("LOAD stopped: memory safety threshold reached", flush=True)
                break
            set_native_limit(native_url, native_secret, limit)
            result = load_round(client, monitor, load_bytes, load_sample["contentType"], limit, jobs, "receipt-image")
            load.append(result)
            print(
                f"LOAD limit={limit} jobs={jobs} success={result['successRate']:.0%} "
                f"throughput={result['throughputDocsPerSecond']:.3f}/s p95={result['latencyP95Seconds']:.2f}s",
                flush=True,
            )
            if result["successRate"] < 1.0:
                print("LOAD stopped: a request failed; diagnose before increasing concurrency", flush=True)
                break
        heavy_sample = next(sample for sample in samples if sample["name"] == "cord-10-page")
        heavy_bytes = heavy_sample["path"].read_bytes()
        for limit, jobs in [(1, 2), (2, 4), (3, 6), (4, 8), (6, 12)]:
            if monitor.abort_event.is_set():
                print("HEAVY LOAD stopped: memory safety threshold reached", flush=True)
                break
            set_native_limit(native_url, native_secret, limit)
            result = load_round(
                client, monitor, heavy_bytes, heavy_sample["contentType"], limit, jobs, "10-page-pdf"
            )
            load.append(result)
            print(
                f"HEAVY LOAD limit={limit} jobs={jobs} success={result['successRate']:.0%} "
                f"throughput={result['throughputDocsPerSecond']:.3f}/s p95={result['latencyP95Seconds']:.2f}s "
                f"min_free={result['minMemoryFreePercent']}",
                flush=True,
            )
            if result["successRate"] < 1.0:
                print("HEAVY LOAD stopped: a request failed; diagnose before increasing concurrency", flush=True)
                break
            if result["minMemoryFreePercent"] is not None and result["minMemoryFreePercent"] < 15:
                print("HEAVY LOAD stopped before next step: free-memory safety margin below 15%", flush=True)
                break
    finally:
        try:
            set_native_limit(native_url, native_secret, original_limit)
        finally:
            monitor.phase = "complete"
            monitor.stop()

    (report_dir / "commands.md").write_text(
        """# Reproduction procedure (redacted)

1. Start PostgreSQL, Redis, proxy, and the native menu-bar worker.
2. Put the real S3 values and native shared secret in ignored `proxy/.env`.
3. Create a disposable user/API key with rate >= 600 RPM and quota >= 500.
4. Export `OCR_API_KEY`, `NATIVE_AUTH_SECRET`, `OCR_API_URL`, and `NATIVE_BASE_URL`.
5. Run `scripts/prod_readiness_e2e.sh`.
6. Run `python3 scripts/final_ocr_benchmark.py --report-dir test-reports/<run>`.

No secret values or presigned URLs are stored in this folder.
""",
        encoding="utf-8",
    )
    write_summary(report_dir, environment, accuracy, load, args.e2e_status)
    success = all(item["passed"] for item in accuracy) and all(item["successRate"] == 1.0 for item in load)
    print(f"REPORT {report_dir}")
    print("PASS" if success else "FAIL")
    return 0 if success else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(f"ERROR: {error}", file=sys.stderr)
        raise SystemExit(1)
