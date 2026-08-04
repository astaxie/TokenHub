package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// recoveryProbeAdapter is a ProviderResourceProber whose outcome the test controls.
// It deliberately does not implement ProviderAdapter: probing resolves the adapter
// through the registry, so the probe path must not require the chat surface.
type recoveryProbeAdapter struct {
	err   error
	calls *int
}

func (a recoveryProbeAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Model: "probe-model", Prompt: "ping"}
}

func (a recoveryProbeAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	if a.calls != nil {
		*a.calls++
	}
	if a.err != nil {
		return ProviderProbeResult{}, a.err
	}
	return ProviderProbeResult{ResourceID: resource.ID, Model: request.Model, OutputText: "pong"}, nil
}

// newProbeRecoveryServer builds a store holding one active provider resource backed by
// a probe-capable adapter, with the failure threshold lowered so tests can trip the
// breaker in two calls.
func newProbeRecoveryServer(t *testing.T, probeErr error, calls *int) (*GormStore, *Server, string) {
	t.Helper()
	store := NewMemoryStore()
	store.failureThreshold = 2
	provider := store.AddProvider(Provider{
		ID:      "prv_probe",
		Name:    "Probe Provider",
		Type:    "probe_test",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_probe",
		ProviderID:   provider.ID,
		Name:         "Probe Instance",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	server.adapterRegistry.Register("probe_test", recoveryProbeAdapter{err: probeErr, calls: calls}, AdapterCapabilityProbe)
	return store, server, resource.ID
}

// tripBreaker drives the resource into cooldown through the same call the request
// path uses, so the test starts from a state the gateway can actually produce.
func tripBreaker(t *testing.T, store *GormStore, resourceID string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})
	}
	resource := findResource(t, store, resourceID)
	if resource.Healthy || resource.CooldownUntil == nil {
		t.Fatalf("expected resource in cooldown, got healthy=%v cooldown=%v", resource.Healthy, resource.CooldownUntil)
	}
}

func TestProbeSuccessRecoversCoolingDownResource(t *testing.T) {
	store, server, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)

	if _, err := server.integrations.TestProviderResource(context.Background(), resourceID, nil); err != nil {
		t.Fatalf("probe should succeed: %v", err)
	}

	resource := findResource(t, store, resourceID)
	if !resource.Healthy {
		t.Fatalf("passing probe must restore health, got healthy=%v", resource.Healthy)
	}
	if resource.FailureCount != 0 {
		t.Fatalf("passing probe must reset failure count, got %d", resource.FailureCount)
	}
	if resource.CooldownUntil != nil {
		t.Fatalf("passing probe must clear cooldown, got %v", resource.CooldownUntil)
	}
	if resource.LastCheckedAt == nil {
		t.Fatal("passing probe must stamp last_checked_at")
	}
}

func TestProbeFailureLeavesResourceUnhealthy(t *testing.T) {
	probeErr := NewHTTPError(http.StatusUnauthorized, "provider_auth_failed", "bad credentials")
	store, server, resourceID := newProbeRecoveryServer(t, probeErr, nil)
	tripBreaker(t, store, resourceID)

	if _, err := server.integrations.TestProviderResource(context.Background(), resourceID, nil); err == nil {
		t.Fatal("probe should fail")
	}

	resource := findResource(t, store, resourceID)
	if resource.Healthy {
		t.Fatal("failing probe must not restore health")
	}
	if resource.CooldownUntil == nil {
		t.Fatal("failing probe must leave the resource in cooldown")
	}
}

// A resource an admin disabled must stay disabled even if the upstream answers.
func TestProbeSuccessLeavesDisabledResourceUnhealthy(t *testing.T) {
	store, server, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resourceID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := server.integrations.TestProviderResource(context.Background(), resourceID, nil); err != nil {
		t.Fatalf("probe should succeed: %v", err)
	}

	resource := findResource(t, store, resourceID)
	if resource.Healthy {
		t.Fatal("a disabled resource must not be recovered by a passing probe")
	}
}

// A mid-stream client disconnect is graded neutral: it must not count against the
// resource, but it is no evidence the upstream works, so it must not close a
// half-open trial either.
func TestNeutralAttemptDoesNotRecoverCoolingDownResource(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	before := findResource(t, store, resourceID)

	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptNeutral, Usage{TotalTokens: 3})

	resource := findResource(t, store, resourceID)
	if resource.Healthy {
		t.Fatal("a neutral attempt must not resurrect a cooling-down resource")
	}
	if resource.CooldownUntil == nil {
		t.Fatal("cooldown must survive a neutral attempt")
	}
	if !resource.CooldownUntil.Equal(*before.CooldownUntil) {
		t.Fatalf("cooldown deadline must not move, before=%v after=%v", before.CooldownUntil, resource.CooldownUntil)
	}
}

// Only the request holding the half-open permit may close the breaker.
func TestSucceededAttemptWithClaimRecoversResource(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	expireCooldown(t, store, resourceID)

	_, claimCtx, err := store.CheckProviderResourceCapacity(context.Background(), resourceID)
	if err != nil {
		t.Fatalf("the trial must be admitted: %v", err)
	}
	if !hasHalfOpenClaim(claimCtx) {
		t.Fatal("admission must mark the context as the half-open claimant")
	}

	store.FinishProviderResourceAttempt(claimCtx, resourceID, "", AttemptSucceeded, Usage{TotalTokens: 3})

	resource := findResource(t, store, resourceID)
	if !resource.Healthy {
		t.Fatal("the claimant's confirmed success must close the breaker")
	}
	if resource.CooldownUntil != nil {
		t.Fatalf("recovery must clear cooldown, got %v", resource.CooldownUntil)
	}
	if resource.FailureCount != 0 {
		t.Fatalf("recovery must reset the failure count, got %d", resource.FailureCount)
	}
}

// The race Codex flagged: requests A and B are both in flight while the resource is
// healthy, A fails and trips the breaker, then B returns successfully. B never held a
// trial permit, so its success must not resurrect the resource mid-cooldown.
func TestSucceededAttemptWithoutClaimDoesNotRecoverResource(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	before := findResource(t, store, resourceID)

	// No claim in this context: this is the straggler completing after the trip.
	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptSucceeded, Usage{TotalTokens: 3})

	resource := findResource(t, store, resourceID)
	if resource.Healthy {
		t.Fatal("a success from a request that never held the permit must not close the breaker")
	}
	if resource.CooldownUntil == nil || !resource.CooldownUntil.Equal(*before.CooldownUntil) {
		t.Fatalf("the cooldown deadline must not move, before=%v after=%v", before.CooldownUntil, resource.CooldownUntil)
	}
	if resource.FailureCount != before.FailureCount {
		t.Fatalf("the backoff counter must not be reset, before=%d after=%d", before.FailureCount, resource.FailureCount)
	}
}

// A neutral outcome adds no failure but clears none either. Clearing would let an
// alternating failure/disconnect pattern keep the breaker from ever tripping.
func TestNeutralAttemptLeavesFailureCountUntouched(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)

	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})
	if got := findResource(t, store, resourceID).FailureCount; got != 1 {
		t.Fatalf("expected one recorded failure, got %d", got)
	}

	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptNeutral, Usage{})
	resource := findResource(t, store, resourceID)
	if resource.FailureCount != 1 {
		t.Fatalf("a neutral attempt must neither add nor clear a failure, got %d", resource.FailureCount)
	}
	if !resource.Healthy {
		t.Fatal("a neutral attempt must not affect health")
	}

	// An alternating pattern must still reach the threshold.
	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})
	if resource := findResource(t, store, resourceID); resource.Healthy {
		t.Fatal("failure, neutral, failure must still trip a threshold of 2")
	}
}

// An ordinary success on a live resource still resets the consecutive failure run.
func TestSucceededAttemptResetsFailureCountWhileHealthy(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)

	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptSucceeded, Usage{})

	resource := findResource(t, store, resourceID)
	if resource.FailureCount != 0 {
		t.Fatalf("a success on a healthy resource must reset the failure count, got %d", resource.FailureCount)
	}
	if !resource.Healthy {
		t.Fatal("the resource must stay healthy")
	}
}

func TestRecoverProviderResourceIsIdempotent(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)

	first, err := store.RecoverProviderResource(resourceID)
	if err != nil {
		t.Fatalf("recovering a healthy resource should be a no-op, got %v", err)
	}
	if !first.Healthy {
		t.Fatal("expected healthy resource to stay healthy")
	}

	tripBreaker(t, store, resourceID)
	recovered, err := store.RecoverProviderResource(resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Healthy || recovered.FailureCount != 0 || recovered.CooldownUntil != nil {
		t.Fatalf("expected full recovery, got healthy=%v failures=%d cooldown=%v",
			recovered.Healthy, recovered.FailureCount, recovered.CooldownUntil)
	}
	if recovered.LastCheckedAt == nil || time.Since(*recovered.LastCheckedAt) > time.Minute {
		t.Fatalf("expected a fresh last_checked_at, got %v", recovered.LastCheckedAt)
	}
}

func TestRecoverProviderResourceRejectsUnknownID(t *testing.T) {
	store, _, _ := newProbeRecoveryServer(t, nil, nil)

	if _, err := store.RecoverProviderResource("rsrc_missing"); err == nil {
		t.Fatal("expected an error for an unknown resource")
	}
}

func TestCooldownWindowBackoff(t *testing.T) {
	store := NewMemoryStore()
	store.failureThreshold = 3
	store.cooldownDuration = 100 * time.Second
	store.cooldownMax = 800 * time.Second

	cases := []struct {
		name         string
		failureCount int
		want         time.Duration
	}{
		{"first trip uses the base window", 3, 100 * time.Second},
		{"second doubles", 4, 200 * time.Second},
		{"third doubles again", 5, 400 * time.Second},
		{"fourth reaches the cap", 6, 800 * time.Second},
		{"further failures stay capped", 7, 800 * time.Second},
		{"an absurd count cannot overflow", 1 << 30, 800 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.cooldownWindow(tc.failureCount); got != tc.want {
				t.Fatalf("cooldownWindow(%d) = %v, want %v", tc.failureCount, got, tc.want)
			}
		})
	}
}

func TestCooldownWindowHandlesDegenerateConfig(t *testing.T) {
	store := NewMemoryStore()
	store.failureThreshold = 2
	store.cooldownDuration = 300 * time.Second

	store.cooldownMax = 0
	if got := store.cooldownWindow(9); got != 300*time.Second {
		t.Fatalf("an unset cap must fall back to the base window, got %v", got)
	}
	store.cooldownMax = 60 * time.Second
	if got := store.cooldownWindow(9); got != 300*time.Second {
		t.Fatalf("a cap below the base must fall back to the base window, got %v", got)
	}
	store.cooldownDuration = 0
	if got := store.cooldownWindow(9); got != 0 {
		t.Fatalf("a zero base disables cooldown, got %v", got)
	}
}

func TestFailureCountSaturates(t *testing.T) {
	store := NewMemoryStore()
	store.failureThreshold = 3
	store.cooldownDuration = 100 * time.Second
	store.cooldownMax = 800 * time.Second

	ceiling := store.failureCountCeiling()
	if got := store.cooldownWindow(ceiling); got != store.cooldownMax {
		t.Fatalf("the ceiling must already reach the cap, got %v", got)
	}
	if next := store.nextFailureCount(ceiling); next != ceiling {
		t.Fatalf("failure count must saturate at %d, got %d", ceiling, next)
	}
	if next := store.nextFailureCount(ceiling + 5); next != ceiling {
		t.Fatalf("failure count must clamp back to %d, got %d", ceiling, next)
	}
}

// expireCooldown backdates the cooldown deadline so the resource is due for a trial.
func expireCooldown(t *testing.T, store *GormStore, resourceID string) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Second)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resourceID).
		Update("cooldown_until", &past).Error; err != nil {
		t.Fatal(err)
	}
}

func TestHalfOpenAdmitsSingleTrial(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	expireCooldown(t, store, resourceID)

	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resourceID); err != nil {
		t.Fatalf("the first request after the cooldown lapses must be admitted: %v", err)
	}
	_, _, err := store.CheckProviderResourceCapacity(context.Background(), resourceID)
	if AsHTTPError(err).Code != "provider_resource_cooling_down" {
		t.Fatalf("a second concurrent trial must be rejected, got %v", err)
	}
}

func TestHalfOpenRearmsCooldownOnClaim(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	expireCooldown(t, store, resourceID)

	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resourceID); err != nil {
		t.Fatal(err)
	}
	resource := findResource(t, store, resourceID)
	if resource.CooldownUntil == nil || !resource.CooldownUntil.After(time.Now().UTC()) {
		t.Fatalf("claiming the trial must arm the next window, got %v", resource.CooldownUntil)
	}
	if resource.Healthy {
		t.Fatal("claiming a trial must not mark the resource healthy on its own")
	}
}

func TestHalfOpenSkipsUnexpiredCooldown(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)

	_, _, err := store.CheckProviderResourceCapacity(context.Background(), resourceID)
	if AsHTTPError(err).Code != "provider_resource_cooling_down" {
		t.Fatalf("a resource still inside its cooldown must be rejected, got %v", err)
	}
}

func TestHalfOpenCandidateSelection(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, ProviderMock)
	store.failureThreshold = 2
	_ = secret
	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resourceID, "", AttemptFailed, Usage{})

	if _, err := store.SelectRouteCandidates("gpt-4.1-mini"); err == nil {
		t.Fatal("a resource inside its cooldown must not be offered as a candidate")
	}

	expireCooldown(t, store, resourceID)
	candidates, err := store.SelectRouteCandidates("gpt-4.1-mini")
	if err != nil {
		t.Fatalf("an expired cooldown must make the resource reachable again: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected the parked resource back as a candidate, got %d", len(candidates))
	}
}

func TestHalfOpenNeverAdmitsDisabledResource(t *testing.T) {
	store, _, resourceID := newProbeRecoveryServer(t, nil, nil)
	tripBreaker(t, store, resourceID)
	expireCooldown(t, store, resourceID)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resourceID).
		Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}

	resource := findResource(t, store, resourceID)
	if halfOpenEligible(resource, time.Now().UTC()) && resource.Status == StatusActive {
		t.Fatal("a disabled resource must never be eligible")
	}
	if _, err := store.RecoverProviderResource(resourceID); err != nil {
		t.Fatal(err)
	}
	if findResource(t, store, resourceID).Healthy {
		t.Fatal("recovery must refuse an admin-disabled resource")
	}
}

// flakyAdapter fails until the test flips it, so one gateway test can cover the whole
// breaker lifecycle: trip, park, half-open trial, recover.
type flakyAdapter struct {
	failing *bool
	calls   *int
}

func (a flakyAdapter) fail() error {
	return NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a flakyAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	if a.calls != nil {
		*a.calls++
	}
	if *a.failing {
		return nil, Usage{}, a.fail()
	}
	return MockAdapter{}.Chat(ctx, provider, providerModel, req)
}

func (a flakyAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	if *a.failing {
		return Usage{}, a.fail()
	}
	return MockAdapter{}.ChatStream(ctx, provider, providerModel, req, w)
}

func (a flakyAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	if *a.failing {
		return nil, Usage{}, a.fail()
	}
	return MockAdapter{}.Responses(ctx, provider, providerModel, req)
}

func (a flakyAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	if *a.failing {
		return nil, Usage{}, a.fail()
	}
	return MockAdapter{}.Embeddings(ctx, provider, providerModel, req)
}

func TestGatewayRecoversAfterCooldownExpires(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, "flaky_resource")
	store.failureThreshold = 2
	failing := true
	upstreamCalls := 0
	server := New(store)
	registerTestAdapter(server, "flaky_resource", flakyAdapter{failing: &failing, calls: &upstreamCalls})
	app := server.Handler()

	chat := func() int {
		return doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model":    "gpt-4.1-mini",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		}, secret).Code
	}

	for i := 0; i < 2; i++ {
		if code := chat(); code != http.StatusBadGateway {
			t.Fatalf("request %d: expected 502 from the failing upstream, got %d", i+1, code)
		}
	}
	if resource := findResource(t, store, resourceID); resource.Healthy || resource.CooldownUntil == nil {
		t.Fatalf("expected the breaker to trip, got healthy=%v cooldown=%v", resource.Healthy, resource.CooldownUntil)
	}

	// While parked, the resource is not routable at all: the request fails before it
	// can reach the upstream.
	callsBeforeParked := upstreamCalls
	if code := chat(); code == http.StatusOK {
		t.Fatal("a parked resource must not serve traffic")
	}
	if upstreamCalls != callsBeforeParked {
		t.Fatalf("a parked resource must not be dialled, calls went %d -> %d", callsBeforeParked, upstreamCalls)
	}

	// Cooldown lapses and the upstream comes back.
	expireCooldown(t, store, resourceID)
	failing = false

	if code := chat(); code != http.StatusOK {
		t.Fatalf("the half-open trial must be served once the upstream recovers, got %d", code)
	}
	resource := findResource(t, store, resourceID)
	if !resource.Healthy {
		t.Fatal("a successful half-open trial must restore health without admin action")
	}
	if resource.CooldownUntil != nil {
		t.Fatalf("recovery must clear the cooldown, got %v", resource.CooldownUntil)
	}
	if resource.FailureCount != 0 {
		t.Fatalf("recovery must reset the failure count, got %d", resource.FailureCount)
	}

	if code := chat(); code != http.StatusOK {
		t.Fatalf("the recovered resource must keep serving, got %d", code)
	}
}

func TestGatewayReparksResourceWhenHalfOpenTrialFails(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, "flaky_resource")
	store.failureThreshold = 2
	failing := true
	server := New(store)
	registerTestAdapter(server, "flaky_resource", flakyAdapter{failing: &failing})
	app := server.Handler()

	chat := func() int {
		return doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model":    "gpt-4.1-mini",
			"messages": []map[string]any{{"role": "user", "content": "hi"}},
		}, secret).Code
	}

	for i := 0; i < 2; i++ {
		chat()
	}
	expireCooldown(t, store, resourceID)

	// The upstream is still broken, so the trial fails and the resource must go back
	// to being parked rather than staying open.
	if code := chat(); code == http.StatusOK {
		t.Fatal("a failing half-open trial must not succeed")
	}
	resource := findResource(t, store, resourceID)
	if resource.Healthy {
		t.Fatal("a failing half-open trial must leave the resource parked")
	}
	if resource.CooldownUntil == nil || !resource.CooldownUntil.After(time.Now().UTC()) {
		t.Fatalf("a failing trial must arm the next window, got %v", resource.CooldownUntil)
	}
}
