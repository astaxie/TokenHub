package perfbench_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/perfbench"
)

func TestRunConcurrentLoadUsesAuthenticatedUniqueRequests(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer benchmark-key" {
			t.Errorf("authorization = %q", got)
		}
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(payload.Messages) != 1 || !strings.Contains(payload.Messages[0].Content, "benchmark_request_") {
			t.Errorf("request was not uniquely identified: %+v", payload)
		}
		requests.Add(1)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		Label:        "tokenhub-concurrency",
		BaseURL:      server.URL,
		APIKey:       "benchmark-key",
		Model:        "benchmark-model",
		Protocol:     perfbench.ProtocolChat,
		Mode:         perfbench.ModeConcurrency,
		Concurrency:  2,
		Duration:     60 * time.Millisecond,
		Warmup:       10 * time.Millisecond,
		Timeout:      time.Second,
		RequestBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Requests == 0 || result.Summary.Failures != 0 || result.Summary.Successes != result.Summary.Requests {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if requests.Load() <= int64(result.Summary.Requests) {
		t.Fatalf("warmup requests were not sent: total=%d measured=%d", requests.Load(), result.Summary.Requests)
	}
	if result.Config.APIKey != "" {
		t.Fatalf("result retained API key")
	}
	if result.Metadata.GoVersion == "" || result.Metadata.OS == "" || result.Metadata.Arch == "" || result.Metadata.CPUCount == 0 {
		t.Fatalf("missing runtime metadata: %+v", result.Metadata)
	}
}

func TestRunRedactsTargetURLFromExportedResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	target.User = url.UserPassword("private-user", "private-password")

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL: target.String(), Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeConcurrency,
		Concurrency: 1, Duration: 10 * time.Millisecond, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.BaseURL != "" {
		t.Fatalf("result retained base URL %q", result.Config.BaseURL)
	}
	exported, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"base_url", "private-user", "private-password", target.Host} {
		if strings.Contains(string(exported), secret) {
			t.Fatalf("exported result contains %q: %s", secret, exported)
		}
	}
}

func TestRunStreamingMeasuresTimeToFirstByte(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{
		Latency:       3 * time.Millisecond,
		StreamChunks:  2,
		ChunkInterval: time.Millisecond,
		ResponseBytes: 32,
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		Label:       "tokenhub-stream",
		BaseURL:     server.URL,
		Model:       "benchmark-model",
		Protocol:    perfbench.ProtocolChat,
		Stream:      true,
		Mode:        perfbench.ModeConcurrency,
		Concurrency: 1,
		Duration:    30 * time.Millisecond,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Requests == 0 || result.Summary.Failures != 0 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if result.Summary.TTFTMS.P50 < 2 {
		t.Fatalf("TTFT was not measured from the delayed stream: %+v", result.Summary.TTFTMS)
	}
}

func TestRunRejectsAmbiguousLoadConfiguration(t *testing.T) {
	t.Parallel()

	_, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL:     "http://127.0.0.1:1",
		Protocol:    perfbench.ProtocolChat,
		Mode:        perfbench.ModeRate,
		Rate:        100,
		Concurrency: 2,
		Duration:    time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("expected an ambiguous load configuration error, got %v", err)
	}
}

func TestRunFixedRateReportsGeneratorSaturationWithoutDeadlocking(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL:     server.URL,
		Protocol:    perfbench.ProtocolChat,
		Mode:        perfbench.ModeRate,
		Rate:        200,
		MaxInFlight: 1,
		Duration:    60 * time.Millisecond,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.DropReasons["load_generator_saturated"] == 0 {
		t.Fatalf("expected load generator saturation: %+v", result.Summary)
	}
}

func TestRunFixedRateStopsSchedulingAtDeadline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{BaseURL: server.URL, Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeRate, Rate: 1, Duration: 100 * time.Millisecond, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Requests != 1 {
		t.Fatalf("requests = %d, want exactly the initial request", result.Summary.Requests)
	}
	if result.Summary.AchievedRPS < 5 || result.Summary.AchievedRPS > 20 {
		t.Fatalf("achieved RPS = %.2f, want approximately 10 over the full 100ms offer window", result.Summary.AchievedRPS)
	}
}

func TestRunFixedRateAccountsForEveryScheduledOffer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	const (
		rate     = 100_000
		duration = 20 * time.Millisecond
	)
	result, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL: server.URL, Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeRate,
		Rate: rate, MaxInFlight: 100, Duration: duration, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOffers := int(duration / (time.Second / rate))
	if result.Summary.OfferedRequests != wantOffers {
		t.Fatalf("offered requests = %d, want %d: %+v", result.Summary.OfferedRequests, wantOffers, result.Summary)
	}
	if result.Summary.Requests+result.Summary.DroppedRequests != wantOffers {
		t.Fatalf("scheduled offers were not fully accounted for: %+v", result.Summary)
	}
}

func TestRunFixedRateRecordsSchedulerLagAsGeneratorDrops(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL: server.URL, Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeRate,
		Rate: int(time.Second), MaxInFlight: 1, Duration: time.Millisecond, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.OfferedRequests != 1_000_000 {
		t.Fatalf("offered requests = %d, want 1000000", result.Summary.OfferedRequests)
	}
	if result.Summary.DropReasons["load_generator_missed_schedule"] == 0 {
		t.Fatalf("scheduler lag was not reported: %+v", result.Summary)
	}
}

func TestRunKeepsImplicitMaxInFlightBoundedAtExtremeRate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{
		BaseURL: server.URL, Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeRate,
		Rate: int(time.Second), Duration: time.Nanosecond, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.MaxInFlight != 1000 {
		t.Fatalf("implicit max in flight = %d, want 1000", result.Config.MaxInFlight)
	}
}

func TestRunThroughputIncludesOutstandingRequestDrainTime(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	result, err := perfbench.Run(t.Context(), perfbench.Config{BaseURL: server.URL, Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeConcurrency, Concurrency: 1, Duration: 20 * time.Millisecond, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.AchievedRPS >= 20 {
		t.Fatalf("throughput %.2f did not include request drain time", result.Summary.AchievedRPS)
	}
}

func TestRunPropagatesMidPhaseCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		config perfbench.Config
	}{
		{name: "concurrency", config: perfbench.Config{Mode: perfbench.ModeConcurrency, Concurrency: 1}},
		{name: "rate", config: perfbench.Config{Mode: perfbench.ModeRate, Rate: 100, MaxInFlight: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestStarted := make(chan struct{}, 1)
			releaseHandler := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestStarted <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-releaseHandler:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(releaseHandler) })

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			config := test.config
			config.BaseURL = server.URL
			config.Protocol = perfbench.ProtocolChat
			config.Duration = time.Second
			config.Timeout = time.Second
			done := make(chan error, 1)
			go func() {
				_, err := perfbench.Run(ctx, config)
				done <- err
			}()

			select {
			case <-requestStarted:
				cancel()
			case <-time.After(time.Second):
				t.Fatal("benchmark request did not start")
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("benchmark did not drain after cancellation")
			}
		})
	}
}
