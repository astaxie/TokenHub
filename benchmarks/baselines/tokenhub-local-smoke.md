# Performance benchmark: tokenhub-local-smoke

- Generated: 2026-08-10T10:11:06Z
- Commit: `cbad27b17bf4d357fd7099352ce993bb55ac8c48`
- Runtime: `go1.26.4 darwin/arm64`, 18 CPUs, Apple M5 Pro, 48.0 GiB RAM
- Scenario: `chat`, stream=false, concurrency=8, duration=2s, request=256 bytes

| Metric | Value |
| --- | ---: |
| Requests | 2191 |
| Success rate | 100.000% |
| Achieved throughput | 1092.11 requests/s |
| Latency P50 / P95 / P99 | 7.238 / 8.346 / 9.227 ms |
| Estimated gateway overhead P50 / P95 / P99 | 2.238 / 3.346 / 4.227 ms |

Estimated gateway overhead is end-to-end client latency minus configured fake-upstream latency, clamped at zero. It is an estimate, not an internal timer.
