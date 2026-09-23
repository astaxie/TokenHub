package perfbench_test

import (
	"testing"
	"time"

	"tokenhub/backend/internal/perfbench"
)

func TestAnalyzeReportsLatencyThroughputAndEstimatedOverhead(t *testing.T) {
	t.Parallel()

	result := perfbench.Analyze(
		perfbench.Config{
			Label:                   "tokenhub-smoke",
			Protocol:                perfbench.ProtocolChat,
			ExpectedUpstreamLatency: 2 * time.Millisecond,
		},
		perfbench.Metadata{GoVersion: "go-test", OS: "test", Arch: "test", CPUCount: 4},
		[]perfbench.Observation{
			{Duration: time.Millisecond, StatusCode: 200, ResponseBytes: 10},
			{Duration: 2 * time.Millisecond, StatusCode: 200, ResponseBytes: 20},
			{Duration: 3 * time.Millisecond, StatusCode: 200, ResponseBytes: 30},
			{Duration: 4 * time.Millisecond, StatusCode: 429, ResponseBytes: 40, Error: "HTTP 429"},
			{Duration: 5 * time.Millisecond, StatusCode: 200, ResponseBytes: 50},
		},
		2*time.Second,
	)

	if result.Summary.Requests != 5 || result.Summary.Successes != 4 || result.Summary.Failures != 1 {
		t.Fatalf("unexpected request counts: %+v", result.Summary)
	}
	if result.Summary.SuccessRatePercent != 80 {
		t.Fatalf("success rate = %v, want 80", result.Summary.SuccessRatePercent)
	}
	if result.Summary.AchievedRPS != 2.5 {
		t.Fatalf("achieved RPS = %v, want 2.5", result.Summary.AchievedRPS)
	}
	if result.Summary.LatencyMS.Mean != 3 || result.Summary.LatencyMS.P50 != 3 || result.Summary.LatencyMS.P95 != 5 || result.Summary.LatencyMS.P99 != 5 {
		t.Fatalf("unexpected latency statistics: %+v", result.Summary.LatencyMS)
	}
	if result.Summary.EstimatedGatewayOverheadMS.Mean != 1.2 || result.Summary.EstimatedGatewayOverheadMS.P50 != 1 || result.Summary.EstimatedGatewayOverheadMS.P99 != 3 {
		t.Fatalf("unexpected overhead statistics: %+v", result.Summary.EstimatedGatewayOverheadMS)
	}
	if result.Summary.ResponseBytes != 150 {
		t.Fatalf("response bytes = %d, want 150", result.Summary.ResponseBytes)
	}
	if result.Summary.StatusCodeCounts[200] != 4 || result.Summary.StatusCodeCounts[429] != 1 {
		t.Fatalf("unexpected status counts: %+v", result.Summary.StatusCodeCounts)
	}
	if result.Summary.DropReasons["HTTP 429"] != 1 {
		t.Fatalf("unexpected drop reasons: %+v", result.Summary.DropReasons)
	}
}

func TestAnalyzeExcludesGeneratorDropsFromGatewayStatistics(t *testing.T) {
	t.Parallel()

	result := perfbench.Analyze(perfbench.Config{}, perfbench.Metadata{}, []perfbench.Observation{
		{Duration: 10 * time.Millisecond, StatusCode: 200},
		{Duration: 20 * time.Millisecond, StatusCode: 500, Error: "HTTP 500"},
		{Dropped: true, Error: "load_generator_saturated"},
	}, time.Second)

	if result.Summary.OfferedRequests != 3 || result.Summary.Requests != 2 || result.Summary.DroppedRequests != 1 {
		t.Fatalf("unexpected offered/completed/dropped counts: %+v", result.Summary)
	}
	if result.Summary.AchievedRPS != 2 || result.Summary.LatencyMS.Mean != 15 {
		t.Fatalf("drop polluted throughput or latency: %+v", result.Summary)
	}
	if result.Summary.SuccessRatePercent < 33.3 || result.Summary.SuccessRatePercent > 33.4 {
		t.Fatalf("success rate does not include offered drops: %v", result.Summary.SuccessRatePercent)
	}
}

func TestAnalyzeCountsAggregatedMissedOffers(t *testing.T) {
	t.Parallel()

	result := perfbench.Analyze(perfbench.Config{}, perfbench.Metadata{}, []perfbench.Observation{
		{Duration: time.Millisecond, StatusCode: 200},
		{Dropped: true, DroppedCount: 9, Error: "load_generator_missed_schedule"},
	}, time.Second)

	if result.Summary.OfferedRequests != 10 || result.Summary.Requests != 1 || result.Summary.DroppedRequests != 9 {
		t.Fatalf("unexpected aggregated offer counts: %+v", result.Summary)
	}
	if result.Summary.DropReasons["load_generator_missed_schedule"] != 9 || result.Summary.SuccessRatePercent != 10 {
		t.Fatalf("unexpected aggregated drop accounting: %+v", result.Summary)
	}
}

func TestAnalyzeSeparatesStreamingTTFTAndTotalUpstreamTime(t *testing.T) {
	t.Parallel()

	result := perfbench.Analyze(perfbench.Config{
		Stream:                  true,
		ExpectedUpstreamLatency: 13 * time.Millisecond,
		ExpectedUpstreamTTFT:    5 * time.Millisecond,
	}, perfbench.Metadata{}, []perfbench.Observation{{
		Duration: 18 * time.Millisecond, TTFT: 7 * time.Millisecond, StatusCode: 200,
	}}, time.Second)

	if result.Summary.EstimatedGatewayOverheadMS.P50 != 5 || result.Summary.EstimatedGatewayTTFTMS.P50 != 2 {
		t.Fatalf("unexpected total/TTFT overhead: %+v", result.Summary)
	}
}
