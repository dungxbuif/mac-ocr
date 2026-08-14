# Performance Benchmarking Policy

No engine- or hardware-specific benchmark is published as part of the public OCR Platform documentation.

Performance depends on document type, page count, image dimensions, recognition options, worker capacity, storage latency, and deployment topology. Any benchmark used for capacity planning should record:

- Dataset composition and whether it contains images or PDF documents.
- Input byte size, pixel dimensions, and page counts.
- Recognition options and language configuration.
- End-to-end queue latency, processing latency, and total latency.
- Success/failure rate and worker concurrency.
- Proxy, storage, database, and worker resource utilization.

Report median, p95, and p99 values. Do not present development-simulator timings as OCR accuracy or production-throughput results.
