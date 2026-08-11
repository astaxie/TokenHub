package perfbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxInFlight      = 1000
	maxRateResultBufferSize = 1000
)

func Run(ctx context.Context, config Config) (Result, error) {
	config = withRunDefaults(config)
	if err := validateRunConfig(config); err != nil {
		return Result{}, err
	}
	client := config.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = max(100, config.MaxInFlight)
		transport.MaxIdleConnsPerHost = max(100, config.MaxInFlight)
		client = &http.Client{Transport: transport}
	}
	runner := loadRunner{config: config, client: client}
	if config.Warmup > 0 {
		if _, _, err := runner.runPhase(ctx, config.Warmup); err != nil {
			return Result{}, fmt.Errorf("warmup: %w", err)
		}
	}
	observations, elapsed, err := runner.runPhase(ctx, config.Duration)
	if err != nil {
		return Result{}, err
	}
	safeConfig := config
	safeConfig.BaseURL = ""
	safeConfig.APIKey = ""
	safeConfig.HTTPClient = nil
	return Analyze(safeConfig, runtimeMetadata(), observations, elapsed), nil
}

func withRunDefaults(config Config) Config {
	if config.Model == "" {
		config.Model = "benchmark-model"
	}
	if config.Protocol == "" {
		config.Protocol = ProtocolChat
	}
	if config.Mode == "" {
		config.Mode = ModeConcurrency
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.RequestBytes <= 0 {
		config.RequestBytes = 256
	}
	if config.MaxInFlight <= 0 {
		config.MaxInFlight = defaultMaxInFlight
	}
	return config
}

func validateRunConfig(config Config) error {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("base URL must be an absolute HTTP or HTTPS URL")
	}
	if config.Duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	if config.Warmup < 0 {
		return fmt.Errorf("warmup cannot be negative")
	}
	if config.Stream && config.Protocol == ProtocolEmbedding {
		return fmt.Errorf("embedding benchmarks do not support streaming")
	}
	switch config.Protocol {
	case ProtocolChat, ProtocolResponses, ProtocolEmbedding:
	default:
		return fmt.Errorf("unsupported protocol %q", config.Protocol)
	}
	switch config.Mode {
	case ModeConcurrency:
		if config.Concurrency <= 0 || config.Rate != 0 {
			return fmt.Errorf("concurrency mode requires positive concurrency and zero rate")
		}
	case ModeRate:
		if config.Rate <= 0 || config.Concurrency != 0 {
			return fmt.Errorf("rate mode requires positive rate and zero concurrency")
		}
		if config.Rate > int(time.Second) {
			return fmt.Errorf("rate cannot exceed %d requests per second", time.Second)
		}
	default:
		return fmt.Errorf("unsupported load mode %q", config.Mode)
	}
	return nil
}

type loadRunner struct {
	config   Config
	client   *http.Client
	sequence atomic.Uint64
}

func (r *loadRunner) runPhase(ctx context.Context, duration time.Duration) ([]Observation, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	started := time.Now()
	deadline := started.Add(duration)
	var observations []Observation
	switch r.config.Mode {
	case ModeConcurrency:
		observations = r.runConcurrency(ctx, deadline)
	case ModeRate:
		observations = r.runRate(ctx, started, deadline)
	default:
		return nil, 0, fmt.Errorf("unsupported load mode %q", r.config.Mode)
	}
	elapsed := time.Since(started)
	if err := ctx.Err(); err != nil {
		return observations, elapsed, err
	}
	return observations, elapsed, nil
}

func (r *loadRunner) runConcurrency(ctx context.Context, deadline time.Time) []Observation {
	results := make(chan Observation, r.config.Concurrency)
	var workers sync.WaitGroup
	for range r.config.Concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for time.Now().Before(deadline) && ctx.Err() == nil {
				results <- r.request(ctx)
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	return collectObservations(results)
}

func (r *loadRunner) runRate(ctx context.Context, started, deadline time.Time) []Observation {
	results := make(chan Observation, rateResultBufferSize(r.config))
	collected := make(chan []Observation, 1)
	go func() { collected <- collectObservations(results) }()
	semaphore := make(chan struct{}, r.config.MaxInFlight)
	var requests sync.WaitGroup
	targetOffers := scheduledOfferCount(deadline.Sub(started), r.config.Rate)
	accountedOffers := 0
	timer := time.NewTimer(max(0, time.Until(started)))
	defer timer.Stop()
	deadlineTimer := time.NewTimer(max(0, time.Until(deadline)))
	defer deadlineTimer.Stop()
loop:
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			break loop
		case <-deadlineTimer.C:
			recordMissedOffers(results, targetOffers-accountedOffers)
			break loop
		case <-timer.C:
			now := time.Now()
			if !now.Before(deadline) {
				recordMissedOffers(results, targetOffers-accountedOffers)
				break loop
			}
			dueOffers := min(targetOffers, scheduledOffersDue(now.Sub(started), r.config.Rate)) - accountedOffers
			if dueOffers <= 0 {
				timer.Reset(max(0, time.Until(started.Add(scheduledOfferOffset(accountedOffers, r.config.Rate)))))
				continue
			}
			recordMissedOffers(results, dueOffers-1)
			select {
			case semaphore <- struct{}{}:
				requests.Add(1)
				go func() {
					defer requests.Done()
					defer func() { <-semaphore }()
					results <- r.request(ctx)
				}()
			default:
				results <- Observation{Error: "load_generator_saturated", Dropped: true}
			}
			accountedOffers += dueOffers
			if accountedOffers >= targetOffers {
				continue
			}
			nextOffer := started.Add(scheduledOfferOffset(accountedOffers, r.config.Rate))
			timer.Reset(max(0, time.Until(nextOffer)))
		}
	}
	go func() {
		requests.Wait()
		close(results)
	}()
	return <-collected
}

func rateResultBufferSize(config Config) int {
	return min(maxRateResultBufferSize, config.MaxInFlight, max(1, config.Rate))
}

func scheduledOfferCount(duration time.Duration, rate int) int {
	whole := int64(duration/time.Second) * int64(rate)
	remainder := int64(duration % time.Second)
	fractional := (remainder*int64(rate) + int64(time.Second) - 1) / int64(time.Second)
	return int(whole + fractional)
}

func scheduledOffersDue(elapsed time.Duration, rate int) int {
	whole := int64(elapsed/time.Second) * int64(rate)
	remainder := int64(elapsed % time.Second)
	return int(whole + remainder*int64(rate)/int64(time.Second) + 1)
}

func scheduledOfferOffset(index, rate int) time.Duration {
	wholeSeconds := index / rate
	remainder := index % rate
	return time.Duration(wholeSeconds)*time.Second + time.Duration(int64(remainder)*int64(time.Second)/int64(rate))
}

func recordMissedOffers(results chan<- Observation, count int) {
	if count > 0 {
		results <- Observation{Error: "load_generator_missed_schedule", Dropped: true, DroppedCount: count}
	}
}

func collectObservations(results <-chan Observation) []Observation {
	observations := make([]Observation, 0)
	for result := range results {
		observations = append(observations, result)
	}
	return observations
}

func (r *loadRunner) request(ctx context.Context) Observation {
	sequence := r.sequence.Add(1)
	payload, err := r.requestPayload(sequence)
	if err != nil {
		return Observation{Error: "payload_error"}
	}
	requestContext, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, r.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return Observation{Error: "request_setup_error"}
	}
	request.Header.Set("content-type", "application/json")
	if r.config.APIKey != "" {
		request.Header.Set("authorization", "Bearer "+r.config.APIKey)
	}
	started := time.Now()
	response, err := r.client.Do(request)
	if err != nil {
		return Observation{Duration: time.Since(started), Error: classifyRequestError(err)}
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	first, firstErr := reader.ReadByte()
	ttft := time.Since(started)
	var responseBytes int64
	if firstErr == nil {
		responseBytes = 1
		copied, copyErr := io.Copy(io.Discard, reader)
		responseBytes += copied
		if copyErr != nil {
			return Observation{Duration: time.Since(started), TTFT: streamTTFT(r.config.Stream, ttft), StatusCode: response.StatusCode, ResponseBytes: responseBytes, Error: "response_read_error"}
		}
	} else if !errors.Is(firstErr, io.EOF) {
		return Observation{Duration: time.Since(started), TTFT: streamTTFT(r.config.Stream, ttft), StatusCode: response.StatusCode, Error: "response_read_error"}
	}
	_ = first
	errorReason := ""
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorReason = fmt.Sprintf("HTTP %d", response.StatusCode)
	}
	return Observation{
		Duration:      time.Since(started),
		TTFT:          streamTTFT(r.config.Stream, ttft),
		StatusCode:    response.StatusCode,
		ResponseBytes: responseBytes,
		Error:         errorReason,
	}
}

func (r *loadRunner) endpoint() string {
	base := strings.TrimRight(r.config.BaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	switch r.config.Protocol {
	case ProtocolResponses:
		return base + "/responses"
	case ProtocolEmbedding:
		return base + "/embeddings"
	default:
		return base + "/chat/completions"
	}
}

func (r *loadRunner) requestPayload(sequence uint64) ([]byte, error) {
	prefix := fmt.Sprintf("benchmark_request_%d_", sequence)
	prompt := prefix + strings.Repeat("x", max(0, r.config.RequestBytes-len(prefix)))
	var payload map[string]any
	switch r.config.Protocol {
	case ProtocolResponses:
		payload = map[string]any{"model": r.config.Model, "input": prompt, "stream": r.config.Stream}
	case ProtocolEmbedding:
		payload = map[string]any{"model": r.config.Model, "input": prompt}
	default:
		payload = map[string]any{
			"model":    r.config.Model,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
			"stream":   r.config.Stream,
		}
	}
	return json.Marshal(payload)
}

func runtimeMetadata() Metadata {
	return Metadata{
		GeneratedAt: time.Now().UTC(),
		GoVersion:   runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUCount:    runtime.NumCPU(),
		CPUModel:    cpuModel(),
		MemoryBytes: systemMemoryBytes(),
	}
}

func cpuModel() string {
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			return strings.TrimSpace(string(output))
		}
	}
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 && (strings.TrimSpace(parts[0]) == "model name" || strings.TrimSpace(parts[0]) == "Hardware") {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return ""
}

func systemMemoryBytes() uint64 {
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			value, _ := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
			return value
		}
	}
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				var kib uint64
				if _, scanErr := fmt.Sscanf(line, "MemTotal: %d kB", &kib); scanErr == nil {
					return kib * 1024
				}
			}
		}
	}
	return 0
}

func streamTTFT(stream bool, duration time.Duration) time.Duration {
	if stream {
		return duration
	}
	return 0
}

func classifyRequestError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "transport_error"
	}
}
