# Performance benchmark: tokenhub-local-stream

- Generated: 2026-08-10T10:11:19Z
- Commit: `cbad27b17bf4d357fd7099352ce993bb55ac8c48`
- Runtime: `go1.26.4 darwin/arm64`, 18 CPUs, Apple M5 Pro, 48.0 GiB RAM
- Scenario: `chat`, stream=true, concurrency=8, duration=2s, request=256 bytes

| Metric | Value |
| --- | ---: |
| Requests | 1065 |
| Success rate | 100.000% |
| Achieved throughput | 528.45 requests/s |
| Latency P50 / P95 / P99 | 14.921 / 16.234 / 19.098 ms |
| TTFT P50 / P95 / P99 | 5.731 / 6.716 / 8.308 ms |
| Estimated gateway TTFT P50 / P95 / P99 | 0.731 / 1.716 / 3.308 ms |
| Estimated gateway overhead P50 / P95 / P99 | 1.921 / 3.234 / 6.098 ms |

Estimated gateway overhead is end-to-end client latency minus configured fake-upstream latency, clamped at zero. It is an estimate, not an internal timer.
