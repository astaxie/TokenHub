package perfbench

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

const SchemaVersion = 1

type Protocol string

const (
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
	ProtocolEmbedding Protocol = "embedding"
)

type LoadMode string

const (
	ModeRate        LoadMode = "rate"
	ModeConcurrency LoadMode = "concurrency"
)

type Config struct {
	Label                   string        `json:"label"`
	BaseURL                 string        `json:"-"`
	APIKey                  string        `json:"-"`
	Model                   string        `json:"model"`
	Protocol                Protocol      `json:"protocol"`
	Stream                  bool          `json:"stream"`
	Mode                    LoadMode      `json:"mode"`
	Rate                    int           `json:"rate,omitempty"`
	Concurrency             int           `json:"concurrency,omitempty"`
	MaxInFlight             int           `json:"max_in_flight,omitempty"`
	Duration                time.Duration `json:"duration"`
	Warmup                  time.Duration `json:"warmup"`
	Timeout                 time.Duration `json:"timeout"`
	RequestBytes            int           `json:"request_bytes"`
	ResponseBytes           int           `json:"response_bytes,omitempty"`
	StreamChunks            int           `json:"stream_chunks,omitempty"`
	ChunkInterval           time.Duration `json:"chunk_interval,omitempty"`
	DeploymentProfile       string        `json:"deployment_profile,omitempty"`
	ExpectedUpstreamLatency time.Duration `json:"expected_upstream_latency"`
	ExpectedUpstreamTTFT    time.Duration `json:"expected_upstream_ttft,omitempty"`
	HTTPClient              *http.Client  `json:"-"`
}

func (c Config) MarshalJSON() ([]byte, error) {
	type configAlias Config
	return json.Marshal(struct {
		configAlias
		Duration                string `json:"duration"`
		Warmup                  string `json:"warmup"`
		Timeout                 string `json:"timeout"`
		ExpectedUpstreamLatency string `json:"expected_upstream_latency"`
		ExpectedUpstreamTTFT    string `json:"expected_upstream_ttft,omitempty"`
		ChunkInterval           string `json:"chunk_interval,omitempty"`
	}{
		configAlias:             configAlias(c),
		Duration:                c.Duration.String(),
		Warmup:                  c.Warmup.String(),
		Timeout:                 c.Timeout.String(),
		ExpectedUpstreamLatency: c.ExpectedUpstreamLatency.String(),
		ExpectedUpstreamTTFT:    c.ExpectedUpstreamTTFT.String(),
		ChunkInterval:           c.ChunkInterval.String(),
	})
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type configAlias Config
	decoded := struct {
		configAlias
		Duration                string `json:"duration"`
		Warmup                  string `json:"warmup"`
		Timeout                 string `json:"timeout"`
		ExpectedUpstreamLatency string `json:"expected_upstream_latency"`
		ExpectedUpstreamTTFT    string `json:"expected_upstream_ttft"`
		ChunkInterval           string `json:"chunk_interval"`
	}{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Config(decoded.configAlias)
	for name, value := range map[string]string{
		"duration": decoded.Duration, "warmup": decoded.Warmup, "timeout": decoded.Timeout,
		"expected_upstream_latency": decoded.ExpectedUpstreamLatency,
		"expected_upstream_ttft":    decoded.ExpectedUpstreamTTFT,
		"chunk_interval":            decoded.ChunkInterval,
	} {
		if value == "" {
			continue
		}
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		switch name {
		case "duration":
			c.Duration = parsed
		case "warmup":
			c.Warmup = parsed
		case "timeout":
			c.Timeout = parsed
		case "expected_upstream_latency":
			c.ExpectedUpstreamLatency = parsed
		case "expected_upstream_ttft":
			c.ExpectedUpstreamTTFT = parsed
		case "chunk_interval":
			c.ChunkInterval = parsed
		}
	}
	return nil
}

type Metadata struct {
	GeneratedAt time.Time `json:"generated_at"`
	GitCommit   string    `json:"git_commit,omitempty"`
	GitDirty    bool      `json:"git_dirty"`
	GoVersion   string    `json:"go_version"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	CPUCount    int       `json:"cpu_count"`
	CPUModel    string    `json:"cpu_model,omitempty"`
	MemoryBytes uint64    `json:"memory_bytes,omitempty"`
}

type Observation struct {
	Duration      time.Duration `json:"-"`
	TTFT          time.Duration `json:"-"`
	StatusCode    int           `json:"status_code"`
	ResponseBytes int64         `json:"response_bytes"`
	Error         string        `json:"error,omitempty"`
	Dropped       bool          `json:"-"`
	DroppedCount  int           `json:"-"`
}

type Distribution struct {
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
}

type Summary struct {
	OfferedRequests            int            `json:"offered_requests"`
	Requests                   int            `json:"requests"`
	DroppedRequests            int            `json:"dropped_requests"`
	Successes                  int            `json:"successes"`
	Failures                   int            `json:"failures"`
	SuccessRatePercent         float64        `json:"success_rate_percent"`
	AchievedRPS                float64        `json:"achieved_rps"`
	LatencyMS                  Distribution   `json:"latency_ms"`
	TTFTMS                     Distribution   `json:"ttft_ms,omitempty"`
	EstimatedGatewayTTFTMS     Distribution   `json:"estimated_gateway_ttft_ms,omitempty"`
	EstimatedGatewayOverheadMS Distribution   `json:"estimated_gateway_overhead_ms"`
	ResponseBytes              int64          `json:"response_bytes"`
	StatusCodeCounts           map[int]int    `json:"status_code_counts"`
	DropReasons                map[string]int `json:"drop_reasons"`
}

type Result struct {
	SchemaVersion int      `json:"schema_version"`
	Config        Config   `json:"config"`
	Metadata      Metadata `json:"metadata"`
	Summary       Summary  `json:"summary"`
}

func Analyze(config Config, metadata Metadata, observations []Observation, elapsed time.Duration) Result {
	latencies := make([]float64, 0, len(observations))
	ttfts := make([]float64, 0, len(observations))
	ttftOverheads := make([]float64, 0, len(observations))
	overheads := make([]float64, 0, len(observations))
	summary := Summary{
		StatusCodeCounts: make(map[int]int),
		DropReasons:      make(map[string]int),
	}
	expectedMS := float64(config.ExpectedUpstreamLatency) / float64(time.Millisecond)
	expectedTTFTMS := float64(config.ExpectedUpstreamTTFT) / float64(time.Millisecond)
	for _, observation := range observations {
		if observation.Dropped {
			dropped := max(1, observation.DroppedCount)
			summary.OfferedRequests += dropped
			summary.DroppedRequests += dropped
			reason := observation.Error
			if reason == "" {
				reason = "load_generator_drop"
			}
			summary.DropReasons[reason] += dropped
			continue
		}
		summary.OfferedRequests++
		summary.Requests++
		latencyMS := float64(observation.Duration) / float64(time.Millisecond)
		latencies = append(latencies, latencyMS)
		if observation.TTFT > 0 {
			ttftMS := float64(observation.TTFT) / float64(time.Millisecond)
			ttfts = append(ttfts, ttftMS)
			ttftOverheads = append(ttftOverheads, math.Max(0, ttftMS-expectedTTFTMS))
		}
		overheads = append(overheads, math.Max(0, latencyMS-expectedMS))
		summary.ResponseBytes += observation.ResponseBytes
		summary.StatusCodeCounts[observation.StatusCode]++
		if observation.StatusCode >= 200 && observation.StatusCode < 300 && observation.Error == "" {
			summary.Successes++
			continue
		}
		summary.Failures++
		reason := observation.Error
		if reason == "" {
			reason = "unknown"
		}
		summary.DropReasons[reason]++
	}
	if summary.OfferedRequests > 0 {
		summary.SuccessRatePercent = float64(summary.Successes) * 100 / float64(summary.OfferedRequests)
	}
	if elapsed > 0 {
		summary.AchievedRPS = float64(summary.Requests) / elapsed.Seconds()
	}
	summary.LatencyMS = distribution(latencies)
	summary.TTFTMS = distribution(ttfts)
	summary.EstimatedGatewayTTFTMS = distribution(ttftOverheads)
	summary.EstimatedGatewayOverheadMS = distribution(overheads)
	return Result{SchemaVersion: SchemaVersion, Config: config, Metadata: metadata, Summary: summary}
}

func distribution(values []float64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	return Distribution{
		Mean: sum / float64(len(sorted)),
		P50:  percentile(sorted, 0.50),
		P95:  percentile(sorted, 0.95),
		P99:  percentile(sorted, 0.99),
		Max:  sorted[len(sorted)-1],
	}
}

func percentile(sorted []float64, quantile float64) float64 {
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}
