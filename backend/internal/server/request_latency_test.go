package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// skewedCall reproduces what StartCall produces on a PostgreSQL deployment whose
// database host runs ahead of the application host: StartedAt is the database
// clock reading, measuredAt is the local reading taken at the same moment.
func skewedCall(requestID string, skew time.Duration) CallContext {
	return CallContext{
		RequestID:  requestID,
		Project:    Project{ID: "project_clock_skew"},
		Key:        APIKey{ID: "key_clock_skew"},
		Model:      Model{Name: "skewed-chat", Modality: "chat", Status: StatusActive},
		StartedAt:  time.Now().UTC().Add(skew),
		measuredAt: time.Now(),
	}
}

func TestFinishCallLatencyIsNotNegativeWhenDatabaseClockRunsAhead(t *testing.T) {
	store := NewMemoryStore()
	route := RouteSelection{Provider: Provider{ID: "provider_clock_skew", Type: ProviderOpenAI}}

	store.FinishCall(skewedCall("req_clock_skew", 4*time.Minute), route, Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}, 200, "", "127.0.0.1", "latency-test")

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	if logs[0].LatencyMS < 0 {
		t.Fatalf("request log latency_ms = %d, want a non-negative value", logs[0].LatencyMS)
	}
	if logs[0].LatencyMS > time.Minute.Milliseconds() {
		t.Fatalf("request log latency_ms = %d, want the locally measured duration", logs[0].LatencyMS)
	}

	observations := store.ListProviderObservations(time.Time{})
	if len(observations) != 1 {
		t.Fatalf("provider observations = %d, want 1", len(observations))
	}
	if observations[0].LatencyMS < 0 {
		t.Fatalf("provider observation latency_ms = %d, want a non-negative value", observations[0].LatencyMS)
	}
}

func TestRecordPlaygroundRequestLatencyIsNotNegativeWhenDatabaseClockRunsAhead(t *testing.T) {
	store := NewMemoryStore()
	route := RouteSelection{Provider: Provider{ID: "provider_playground_skew", Type: ProviderOpenAI}}

	store.RecordPlaygroundRequest(skewedCall("req_playground_skew", 4*time.Minute), route, 200, "", "127.0.0.1", "latency-test")

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	if logs[0].LatencyMS < 0 || logs[0].LatencyMS > time.Minute.Milliseconds() {
		t.Fatalf("playground latency_ms = %d, want the locally measured duration", logs[0].LatencyMS)
	}
}

func TestCompleteImageJobLatencyIsNotNegativeWhenDatabaseClockRunsAhead(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Clock Skew"})
	call := skewedCall("req_image_skew", 4*time.Minute)
	call.Project = project
	call.Model = Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive}
	provider := store.AddProvider(Provider{
		ID: "prv_image_skew", Name: "Image Skew Provider", Type: ProviderOpenAI, Status: StatusActive, Healthy: true,
	})

	job, err := store.CreateImageJob(ImageJob{
		ProjectID: project.ID, RequestID: call.RequestID,
		Status: imageJobStatusQueued, Model: call.Model.Name, Action: "generate",
	}, "skewed image prompt")
	if err != nil {
		t.Fatal(err)
	}
	job, claimed, err := store.ClaimImageJob(job.ID)
	if err != nil || !claimed {
		t.Fatalf("claim image job: claimed=%v err=%v", claimed, err)
	}
	job.Status = imageJobStatusCompleted

	asset := ImageAsset{
		JobID: job.ID, ProjectID: project.ID, Role: "output",
		RelativePath: "skew/output.png", ContentType: "image/png",
	}
	selection := RouteSelection{Provider: provider, ProviderModel: openAIImageModelName}
	if err := store.CompleteImageJob(call, job, "", asset, selection, Usage{TotalTokens: 1}, "127.0.0.1", "latency-test"); err != nil {
		t.Fatal(err)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	if logs[0].LatencyMS < 0 || logs[0].LatencyMS > time.Minute.Milliseconds() {
		t.Fatalf("image completion latency_ms = %d, want the locally measured duration", logs[0].LatencyMS)
	}
}

func TestFailUnfinishedImageJobsLatencyIsNotNegativeForFutureCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Recovery Skew"})

	// A replica whose clock ran ahead queued this job, so its created_at is in the
	// future for the replica that recovers it after a restart.
	if _, err := store.CreateImageJob(ImageJob{
		ProjectID: project.ID, RequestID: "req_image_recovery_skew",
		Status: imageJobStatusQueued, Model: openAIImageModelName, Action: "generate",
		CreatedAt: time.Now().UTC().Add(4 * time.Minute),
	}, "recovered image prompt"); err != nil {
		t.Fatal(err)
	}

	if _, err := store.FailUnfinishedImageJobs("server_restarted", "server restarted"); err != nil {
		t.Fatal(err)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want 1", len(logs))
	}
	if logs[0].LatencyMS != 0 {
		t.Fatalf("recovered image job latency_ms = %d, want 0", logs[0].LatencyMS)
	}
}

func TestStartCallRecordsLocalLatencyReference(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Latency Reference"})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "latency-reference", Allowed: []string{"reference-chat"}, Status: StatusActive,
	}, "thk_latency_reference")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: "reference-chat", Modality: "chat", Status: StatusActive})

	call, err := store.StartCall(context.Background(), project, key, model.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if call.measuredAt.IsZero() {
		t.Fatal("StartCall must record a local reference so latency does not depend on the database clock")
	}
	if elapsed := call.elapsed(); elapsed < 0 || elapsed > time.Minute {
		t.Fatalf("elapsed = %v, want a small non-negative duration", elapsed)
	}
}

func TestTracingSpanStartsOnTheLocalClockWhenDatabaseClockRunsAhead(t *testing.T) {
	endpoint := newFakeOTLPEndpoint(t)
	app := newTracingTestServer(t, tracingTestConfig(endpoint.tracesURL()))

	call := skewedCall("req_trace_skew", 4*time.Minute)
	finishedAt := time.Now().UTC()
	app.traceEmitter.EmitGatewayCall(GatewayCallCompletion{
		Kind:       CompletionKindRouted,
		Call:       call,
		StatusCode: http.StatusOK,
		FinishedAt: finishedAt,
	})
	flushTracing(t, app)

	spans := endpoint.collected()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	startedAt := time.Unix(0, int64(spans[0].GetStartTimeUnixNano())).UTC()
	endedAt := time.Unix(0, int64(spans[0].GetEndTimeUnixNano())).UTC()
	if startedAt.After(finishedAt) {
		t.Fatalf("span starts at %s, after the call finished at %s", startedAt, finishedAt)
	}
	if endedAt.Sub(startedAt) > time.Minute {
		t.Fatalf("span covers %s, want the locally measured duration", endedAt.Sub(startedAt))
	}
}

func TestCallElapsedNeverReportsNegativeDuration(t *testing.T) {
	for _, testCase := range []struct {
		name string
		call CallContext
		want time.Duration
	}{
		{
			name: "no timestamps at all",
			call: CallContext{},
			want: 0,
		},
		{
			name: "only a future database timestamp",
			call: CallContext{StartedAt: time.Now().UTC().Add(4 * time.Minute)},
			want: 0,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.call.elapsed(); got != testCase.want {
				t.Fatalf("elapsed = %v, want %v", got, testCase.want)
			}
		})
	}

	past := CallContext{StartedAt: time.Now().UTC().Add(-2 * time.Second)}
	if elapsed := past.elapsed(); elapsed < time.Second {
		t.Fatalf("elapsed = %v, want at least the two seconds since StartedAt", elapsed)
	}
}

func TestLatencyMillisClampsNegativeIntervals(t *testing.T) {
	if got := latencyMillis(-4 * time.Minute); got != 0 {
		t.Fatalf("latencyMillis(-4m) = %d, want 0", got)
	}
	if got := latencyMillis(1500 * time.Millisecond); got != 1500 {
		t.Fatalf("latencyMillis(1.5s) = %d, want 1500", got)
	}
}
