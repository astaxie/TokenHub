# Performance benchmark: tokenhub-local-responses

- Generated: 2026-08-10T10:11:24Z
- Commit: `cbad27b17bf4d357fd7099352ce993bb55ac8c48`
- Runtime: `go1.26.4 darwin/arm64`, 18 CPUs, Apple M5 Pro, 48.0 GiB RAM
- Scenario: `responses`, stream=false, concurrency=8, duration=2s, request=256 bytes

| Metric | Value |
| --- | ---: |
| Requests | 2150 |
| Success rate | 100.000% |
| Achieved throughput | 1071.73 requests/s |
| Latency P50 / P95 / P99 | 7.394 / 8.524 / 9.649 ms |
| Estimated gateway overhead P50 / P95 / P99 | 2.394 / 3.524 / 4.649 ms |

Estimated gateway overhead is end-to-end client latency minus configured fake-upstream latency, clamped at zero. It is an estimate, not an internal timer.
