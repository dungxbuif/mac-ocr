# Apple Vision OCR Benchmark — Mac mini M4 Pro

**Ngày đo:** 2026-08-12
**Mục tiêu:** Chọn concurrency mặc định và thiết kế resource governor cho Native OCR Service chạy chung máy với các workload khác.

## Môi trường

| Thành phần | Giá trị |
|---|---|
| Máy | Mac mini (Mac16,11) |
| Chip | Apple M4 Pro, 12 cores (8 performance + 4 efficiency) |
| RAM | 48 GB |
| macOS | 26.5.2 (Build 25F84) |
| Swift | Apple Swift 6.3.3 |
| OCR | `VNRecognizeTextRequest`, `vi-VN` + `en-US` |

Serial number, hardware UUID và các định danh thiết bị không được lưu trong báo cáo.

## Phương pháp

- Tạo ba ảnh synthetic có nội dung hóa đơn Việt/Anh bằng Core Graphics/Core Text.
- Đo riêng `.fast` và `.accurate` sau một lần warm-up cho mỗi fixture/mode.
- Serial latency: 12 lần chạy cho mỗi fixture/mode.
- Concurrency: 24 job A4 synthetic ở concurrency 1, 2 và 4.
- Thời gian đo chỉ bao quanh `VNImageRequestHandler.perform()`.
- Raw data: [`benchmarks/results/2026-08-12-m4-pro.tsv`](benchmarks/results/2026-08-12-m4-pro.tsv).
- Harness tái lập: [`benchmarks/vision_ocr_benchmark.swift`](benchmarks/vision_ocr_benchmark.swift).

## Serial latency

| Fixture | Pixels | Mode | Mean | p50 | p95 |
|---|---:|---|---:|---:|---:|
| Receipt | 1.80 MP | fast | 20.20 ms | 19.92 ms | 21.86 ms |
| Receipt | 1.80 MP | accurate | 265.24 ms | 263.14 ms | 278.20 ms |
| A4 | 8.70 MP | fast | 66.16 ms | 65.53 ms | 70.18 ms |
| A4 | 8.70 MP | accurate | 762.89 ms | 764.61 ms | 780.25 ms |
| Large | 12.00 MP | fast | 63.84 ms | 63.74 ms | 67.24 ms |
| Large | 12.00 MP | accurate | 588.51 ms | 584.61 ms | 603.31 ms |

12 MP nhanh hơn fixture A4 8.7 MP vì lượng/vị trí text khác nhau. Pixel count không đủ để dự báo latency; text density và layout cũng ảnh hưởng.

## Concurrency — A4 8.7 MP

| Mode | Concurrency | Throughput | Mean/job | p50/job | p95/job |
|---|---:|---:|---:|---:|---:|
| fast | 1 | 13.923 job/s | 71.37 ms | 68.02 ms | 81.74 ms |
| fast | 2 | 16.012 job/s | 121.94 ms | 123.17 ms | 133.21 ms |
| fast | 4 | 15.918 job/s | 235.64 ms | 245.40 ms | 267.28 ms |
| accurate | 1 | 1.354 job/s | 737.47 ms | 730.48 ms | 758.47 ms |
| accurate | 2 | 1.361 job/s | 1437.78 ms | 1456.64 ms | 1555.69 ms |
| accurate | 4 | 1.397 job/s | 2682.85 ms | 2829.61 ms | 2906.09 ms |

## Kết luận thiết kế

1. **Mặc định concurrency = 1.** Với `.accurate`, concurrency 2 gần như không tăng throughput nhưng làm p50 tăng xấp xỉ 2 lần; concurrency 4 làm p50 tăng gần 4 lần.
2. **Không tự tăng concurrency theo queue depth.** Máy dùng chung tài nguyên; queue dài không phải bằng chứng máy đang rảnh.
3. **Cho phép operator đổi động từ 0 đến hard ceiling.** `0` nghĩa là pause/drain; giảm limit không kill job đang chạy.
4. **Native quyết định admission atomically.** Proxy dùng capacity webhook để scheduling nhưng `POST /ocr` vẫn phải `tryAcquire` tại Native.
5. **Completion webhook là tín hiệu dispatch chính.** Native release slot trước, tăng sequence, rồi gửi result kèm capacity snapshot; Proxy có thể đẩy job kế tiếp ngay.
6. `.fast` có thể dùng concurrency 2 nếu ưu tiên throughput, nhưng không nên là mặc định trên shared host.

## Giới hạn của phép đo

Benchmark này chưa bao gồm:

- Upload/download qua mạng.
- Base64 decode, image decode, orientation normalization và downsample.
- PDF render/split page.
- PostgreSQL, Redis, S3 và callback latency.
- Thermal throttling hoặc workload cạnh tranh kéo dài.
- Ảnh thật với blur, rotation, handwriting, nhiều cột hay mật độ text khác nhau.

Vì vậy số liệu là baseline cho admission/concurrency, không phải SLA. Trước production cần chạy corpus thật và soak test trong lúc các workload khác trên máy hoạt động.

## Chạy lại

```bash
swiftc -module-cache-path /private/tmp/mac-ocr-swift-module-cache \
  benchmarks/vision_ocr_benchmark.swift \
  -o /private/tmp/vision-ocr-benchmark

/private/tmp/vision-ocr-benchmark 12 24
```

Hai tham số lần lượt là số mẫu serial và số job cho mỗi mức concurrency.
