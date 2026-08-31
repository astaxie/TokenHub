package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProviderMonitoringSignal struct {
	State       string     `json:"state"`
	Source      string     `json:"source"`
	Detail      string     `json:"detail,omitempty"`
	Samples     int        `json:"samples"`
	SuccessRate float64    `json:"success_rate,omitempty"`
	LatencyMS   int64      `json:"latency_ms,omitempty"`
	ErrorCode   string     `json:"error_code,omitempty"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
}

type ProviderQuotaAccountSummary struct {
	ResourceID   string                       `json:"resource_id"`
	ResourceName string                       `json:"resource_name"`
	Quota        ProviderAccountQuotaSnapshot `json:"quota,omitempty"`
	ErrorCode    string                       `json:"error_code,omitempty"`
}

type ProviderQuotaSummary struct {
	Supported          bool                          `json:"supported"`
	RemainingPercent   float64                       `json:"remaining_percent,omitempty"`
	LimitReached       bool                          `json:"limit_reached"`
	PlanType           string                        `json:"plan_type,omitempty"`
	EarliestResetAt    int64                         `json:"earliest_reset_at,omitempty"`
	FetchedAt          int64                         `json:"fetched_at,omitempty"`
	SuccessfulAccounts int                           `json:"successful_accounts"`
	FailedAccounts     int                           `json:"failed_accounts"`
	Accounts           []ProviderQuotaAccountSummary `json:"accounts,omitempty"`
}

type ProviderMonitoringSnapshot struct {
	Provider             Provider                 `json:"provider"`
	Adapter              AdapterDescriptor        `json:"adapter"`
	RouteCount           int                      `json:"route_count"`
	ActiveRouteCount     int                      `json:"active_route_count"`
	ResourceCount        int                      `json:"resource_count"`
	ActiveResourceCount  int                      `json:"active_resource_count"`
	HealthyResourceCount int                      `json:"healthy_resource_count"`
	State                string                   `json:"state"`
	StatusLabel          string                   `json:"status_label"`
	StatusDetail         string                   `json:"status_detail"`
	Configuration        ProviderMonitoringSignal `json:"configuration"`
	Resources            ProviderMonitoringSignal `json:"resources"`
	ActiveProbe          ProviderMonitoringSignal `json:"active_probe"`
	Gateway              ProviderMonitoringSignal `json:"gateway"`
	Quota                ProviderQuotaSummary     `json:"quota"`
	QualityScore         int                      `json:"quality_score"`
	Trend                []string                 `json:"trend"`
}

func (s *Server) providerMonitoringSnapshots(ctx context.Context, providerID string) []ProviderMonitoringSnapshot {
	providers := s.store.ListProviders()
	resources := s.store.ListProviderResources()
	routes := s.store.ListRoutes()
	observations := s.store.ListProviderObservations(time.Now().UTC().Add(-30 * 24 * time.Hour))
	now := time.Now().UTC()
	snapshots := make([]ProviderMonitoringSnapshot, 0, len(providers))
	for _, provider := range providers {
		if providerID != "" && provider.ID != providerID {
			continue
		}
		descriptor, _ := s.adapterRegistry.Describe(provider.Type)
		snapshot := buildProviderMonitoringSnapshot(now, provider, descriptor, resources, routes, observations)
		snapshots = append(snapshots, snapshot)
	}
	s.populateProviderQuotaSummaries(ctx, snapshots, resources)
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Provider.Priority != snapshots[j].Provider.Priority {
			return snapshots[i].Provider.Priority < snapshots[j].Provider.Priority
		}
		return snapshots[i].Provider.Name < snapshots[j].Provider.Name
	})
	return snapshots
}

func buildProviderMonitoringSnapshot(now time.Time, provider Provider, descriptor AdapterDescriptor, resources []ProviderResource, routes []ModelRoute, observations []ProviderObservation) ProviderMonitoringSnapshot {
	var providerResources []ProviderResource
	activeResources := 0
	healthyResources := 0
	for _, resource := range resources {
		if resource.ProviderID != provider.ID {
			continue
		}
		providerResources = append(providerResources, resource)
		if resource.Status == StatusActive {
			activeResources++
			if resource.Healthy {
				healthyResources++
			}
		}
	}
	routeCount := 0
	activeRouteCount := 0
	for _, route := range routes {
		if route.ProviderID != provider.ID {
			continue
		}
		routeCount++
		if route.Status == StatusActive {
			activeRouteCount++
		}
	}
	var probeObservations []ProviderObservation
	var gatewayObservations []ProviderObservation
	for _, observation := range observations {
		if observation.ProviderID != provider.ID {
			continue
		}
		switch observation.Source {
		case "active_probe":
			probeObservations = append(probeObservations, observation)
		case "gateway_request":
			gatewayObservations = append(gatewayObservations, observation)
		}
	}
	configuration := configurationMonitoringSignal(provider)
	resourceSignal := resourceMonitoringSignal(activeResources, healthyResources)
	activeProbe := observationMonitoringSignal(now, "active_probe", probeObservations)
	gateway := observationMonitoringSignal(now, "gateway_request", gatewayObservations)
	state, detail := aggregateProviderMonitoringState(configuration, resourceSignal, activeProbe, gateway)
	quality := providerMonitoringQuality(state, gateway, activeResources, healthyResources)
	return ProviderMonitoringSnapshot{
		Provider:             provider,
		Adapter:              descriptor,
		RouteCount:           routeCount,
		ActiveRouteCount:     activeRouteCount,
		ResourceCount:        len(providerResources),
		ActiveResourceCount:  activeResources,
		HealthyResourceCount: healthyResources,
		State:                state,
		StatusLabel:          providerMonitoringStatusLabel(state),
		StatusDetail:         detail,
		Configuration:        configuration,
		Resources:            resourceSignal,
		ActiveProbe:          activeProbe,
		Gateway:              gateway,
		Quota:                ProviderQuotaSummary{Supported: adapterSupports(descriptor, AdapterCapabilityQuota)},
		QualityScore:         quality,
		Trend:                providerObservationTrend(now, append(probeObservations, gatewayObservations...)),
	}
}

func configurationMonitoringSignal(provider Provider) ProviderMonitoringSignal {
	if provider.Status != StatusActive {
		return ProviderMonitoringSignal{State: "down", Source: "configuration", Detail: "provider_" + provider.Status}
	}
	if !provider.Healthy {
		return ProviderMonitoringSignal{State: "down", Source: "configuration", Detail: "provider_unhealthy"}
	}
	return ProviderMonitoringSignal{State: "healthy", Source: "configuration", Detail: "provider_online"}
}

func resourceMonitoringSignal(active int, healthy int) ProviderMonitoringSignal {
	if active == 0 {
		return ProviderMonitoringSignal{State: "unknown", Source: "configuration", Detail: "no_active_resources"}
	}
	successRate := float64(healthy) / float64(active) * 100
	switch {
	case healthy == 0:
		return ProviderMonitoringSignal{State: "down", Source: "configuration", Detail: "no_healthy_resources", Samples: active, SuccessRate: successRate}
	case healthy < active:
		return ProviderMonitoringSignal{State: "degraded", Source: "configuration", Detail: "some_resources_unhealthy", Samples: active, SuccessRate: successRate}
	default:
		return ProviderMonitoringSignal{State: "healthy", Source: "configuration", Detail: "resources_healthy", Samples: active, SuccessRate: successRate}
	}
}

func observationMonitoringSignal(now time.Time, source string, observations []ProviderObservation) ProviderMonitoringSignal {
	if len(observations) == 0 {
		return ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "no_samples"}
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].ObservedAt.After(observations[j].ObservedAt) })
	cutoff := now.Add(-24 * time.Hour)
	var recent []ProviderObservation
	for _, observation := range observations {
		if !observation.ObservedAt.Before(cutoff) {
			recent = append(recent, observation)
		}
	}
	if len(recent) == 0 {
		latest := observations[0]
		return ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "samples_stale", ErrorCode: latest.ErrorCode, ObservedAt: &latest.ObservedAt}
	}
	successes := 0
	var successfulLatencies []int64
	for _, observation := range recent {
		if observation.Success {
			successes++
			if observation.LatencyMS > 0 {
				successfulLatencies = append(successfulLatencies, observation.LatencyMS)
			}
		}
	}
	rate := float64(successes) / float64(len(recent)) * 100
	state := "healthy"
	if source == "active_probe" && !recent[0].Success {
		state = "degraded"
	} else if !recent[0].Success && rate < 90 {
		state = "down"
	} else if rate < 99 {
		state = "degraded"
	}
	latest := recent[0]
	return ProviderMonitoringSignal{
		State:       state,
		Source:      source,
		Detail:      "observed",
		Samples:     len(recent),
		SuccessRate: rate,
		LatencyMS:   medianLatency(successfulLatencies),
		ErrorCode:   latest.ErrorCode,
		ObservedAt:  &latest.ObservedAt,
	}
}

func aggregateProviderMonitoringState(configuration ProviderMonitoringSignal, resources ProviderMonitoringSignal, probe ProviderMonitoringSignal, gateway ProviderMonitoringSignal) (string, string) {
	if configuration.State == "down" {
		return "down", "configuration:" + configuration.Detail
	}
	if resources.State == "down" {
		return "down", "configuration:" + resources.Detail
	}
	if gateway.State == "down" {
		return "down", "gateway_request:" + firstNonEmpty(gateway.ErrorCode, gateway.Detail)
	}
	if resources.State == "degraded" || probe.State == "degraded" || gateway.State == "degraded" {
		signal := probe
		if gateway.State == "degraded" {
			signal = gateway
		} else if resources.State == "degraded" {
			signal = resources
		}
		return "degraded", signal.Source + ":" + firstNonEmpty(signal.ErrorCode, signal.Detail)
	}
	if probe.State == "healthy" {
		return "healthy", "active_probe:ok"
	}
	if gateway.State == "healthy" {
		return "healthy", "gateway_request:ok"
	}
	return "unknown", "awaiting_observation"
}

func providerMonitoringStatusLabel(state string) string {
	switch state {
	case "healthy":
		return "Healthy"
	case "degraded":
		return "Degraded"
	case "down":
		return "Functional Down"
	default:
		return "Awaiting Test"
	}
}

func providerMonitoringQuality(state string, gateway ProviderMonitoringSignal, activeResources int, healthyResources int) int {
	resourceScore := 100.0
	if activeResources > 0 {
		resourceScore = float64(healthyResources) / float64(activeResources) * 100
	}
	availability := 95.0
	if gateway.Samples > 0 {
		availability = gateway.SuccessRate
	}
	score := availability*0.7 + resourceScore*0.3
	if state == "down" {
		score = math.Min(score, 45)
	} else if state == "unknown" {
		score = math.Min(score, 80)
	}
	return int(math.Round(math.Max(0, math.Min(100, score))))
}

func medianLatency(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[(len(values)-1)/2]
}

func providerObservationTrend(now time.Time, observations []ProviderObservation) []string {
	trend := make([]string, 30)
	for index := range trend {
		trend[index] = "none"
	}
	for day := 0; day < 30; day++ {
		start := startOfUTCDay(now.AddDate(0, 0, day-29))
		end := start.Add(24 * time.Hour)
		total := 0
		success := 0
		for _, observation := range observations {
			if observation.ObservedAt.Before(start) || !observation.ObservedAt.Before(end) {
				continue
			}
			total++
			if observation.Success {
				success++
			}
		}
		if total == 0 {
			continue
		}
		rate := float64(success) / float64(total) * 100
		switch {
		case rate >= 99:
			trend[day] = "success"
		case rate >= 90:
			trend[day] = "warning"
		default:
			trend[day] = "failure"
		}
	}
	return trend
}

func startOfUTCDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Server) populateProviderQuotaSummaries(ctx context.Context, snapshots []ProviderMonitoringSnapshot, resources []ProviderResource) {
	type result struct {
		providerIndex int
		account       ProviderQuotaAccountSummary
	}
	results := make(chan result)
	var wait sync.WaitGroup
	for providerIndex := range snapshots {
		if !snapshots[providerIndex].Quota.Supported {
			continue
		}
		for _, resource := range resources {
			if resource.ProviderID != snapshots[providerIndex].Provider.ID || resource.Status != StatusActive {
				continue
			}
			wait.Add(1)
			go func(index int, item ProviderResource) {
				defer wait.Done()
				account := ProviderQuotaAccountSummary{ResourceID: item.ID, ResourceName: item.Name}
				quota, err := s.queryProviderMonitoringQuota(ctx, snapshots[index].Provider, item)
				if err != nil {
					account.ErrorCode = AsHTTPError(err).Code
				} else {
					account.Quota = quota
				}
				select {
				case results <- result{providerIndex: index, account: account}:
				case <-ctx.Done():
				}
			}(providerIndex, resource)
		}
	}
	go func() {
		wait.Wait()
		close(results)
	}()
	for item := range results {
		summary := &snapshots[item.providerIndex].Quota
		summary.Accounts = append(summary.Accounts, item.account)
		if len(item.account.Quota) == 0 {
			summary.FailedAccounts++
			continue
		}
		summary.SuccessfulAccounts++
		mergeProviderQuotaSummary(summary, item.account.Quota)
	}
	for index := range snapshots {
		sort.Slice(snapshots[index].Quota.Accounts, func(i, j int) bool {
			return snapshots[index].Quota.Accounts[i].ResourceName < snapshots[index].Quota.Accounts[j].ResourceName
		})
	}
}

type ProviderAccountQuotaSnapshot map[string]any

func (s *Server) queryProviderMonitoringQuota(ctx context.Context, provider Provider, resource ProviderResource) (ProviderAccountQuotaSnapshot, error) {
	if quota, ok := s.cachedProviderMonitoringQuota(resource.ID, providerMonitoringQuotaTTL); ok {
		return quota, nil
	}
	result, handled, err := s.executeProviderCapabilityAction(ctx, AdminUser{ID: "system", Name: "System", Role: "system"}, provider.Type, AdapterCapabilityQuota, "quota.read", map[string]any{
		"resource_id": resource.ID,
		"refresh":     false,
	}, providerPluginActionOptions{
		ApplySideEffects: true,
		ResourceType:     resource.ResourceType,
	})
	if err != nil {
		if AsHTTPError(err).Status >= http.StatusInternalServerError {
			if stale, staleOK := s.cachedProviderMonitoringQuota(resource.ID, 0); staleOK {
				return stale, nil
			}
		}
		return nil, err
	}
	if !handled {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_resource_quota_unsupported", "Quota is not available for this provider resource")
	}
	quota, ok := providerMonitoringQuotaSnapshot(result.Data, time.Now().UTC())
	if !ok {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_quota_invalid_result", "Provider quota returned an invalid result")
	}
	return quota, nil
}

const providerMonitoringQuotaTTL = 60 * time.Second

func (s *Server) cachedProviderMonitoringQuota(resourceID string, ttl time.Duration) (ProviderAccountQuotaSnapshot, bool) {
	observation, ok := s.store.GetProviderResourceObservation(resourceID)
	if !ok || observation.QuotaFetchedAt == nil || strings.TrimSpace(observation.QuotaSnapshot) == "" {
		return nil, false
	}
	if ttl > 0 && time.Since(observation.QuotaFetchedAt.UTC()) > ttl {
		return nil, false
	}
	return providerMonitoringQuotaSnapshotFromJSON(observation.QuotaSnapshot)
}

func providerMonitoringQuotaSnapshot(data any, now time.Time) (ProviderAccountQuotaSnapshot, bool) {
	snapshot, _, _, ok := pluginActionResultQuotaSnapshot(data, now)
	if !ok {
		return nil, false
	}
	return ProviderAccountQuotaSnapshot(snapshot), true
}

func providerMonitoringQuotaSnapshotFromJSON(data string) (ProviderAccountQuotaSnapshot, bool) {
	var snapshot map[string]any
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&snapshot); err != nil || len(snapshot) == 0 {
		return nil, false
	}
	return ProviderAccountQuotaSnapshot(snapshot), true
}

func mergeProviderQuotaSummary(summary *ProviderQuotaSummary, quota ProviderAccountQuotaSnapshot) {
	remaining := providerQuotaRemainingPercent(quota)
	if summary.SuccessfulAccounts == 1 || remaining < summary.RemainingPercent {
		summary.RemainingPercent = remaining
		summary.PlanType = quotaString(quota["plan_type"])
	}
	if fetchedAt := quotaInt64(quota["fetched_at"]); fetchedAt > summary.FetchedAt {
		summary.FetchedAt = fetchedAt
	}
	summary.LimitReached = summary.LimitReached || providerQuotaLimitReached(quota)
	for _, window := range providerQuotaWindows(quota) {
		resetAt := quotaInt64(window["reset_at"])
		if resetAt <= 0 {
			continue
		}
		if summary.EarliestResetAt == 0 || resetAt < summary.EarliestResetAt {
			summary.EarliestResetAt = resetAt
		}
	}
}

func providerQuotaRemainingPercent(quota ProviderAccountQuotaSnapshot) float64 {
	if remaining, ok := quotaFloat64(quota["remaining_percent"]); ok {
		return math.Max(0, math.Min(100, remaining))
	}
	if used, ok := quotaFloat64(quota["used_percent"]); ok {
		return math.Max(0, math.Min(100, 100-used))
	}
	used := maxProviderQuotaUsedPercent(providerQuotaWindows(quota))
	if used == 0 && providerQuotaLimitReached(quota) {
		return 0
	}
	return math.Max(0, math.Min(100, 100-used))
}

func providerQuotaLimitReached(quota ProviderAccountQuotaSnapshot) bool {
	if limited, ok := quotaBool(quota["limit_reached"]); ok && limited {
		return true
	}
	if allowed, ok := quotaBool(quota["allowed"]); ok && !allowed {
		return true
	}
	if rateLimit, ok := quotaObject(quota["rate_limit"]); ok {
		if limited, ok := quotaBool(rateLimit["limit_reached"]); ok && limited {
			return true
		}
		if allowed, ok := quotaBool(rateLimit["allowed"]); ok && !allowed {
			return true
		}
	}
	return false
}

func providerQuotaWindows(quota ProviderAccountQuotaSnapshot) []map[string]any {
	var windows []map[string]any
	for _, key := range []string{"primary_window", "secondary_window"} {
		if window, ok := quotaObject(quota[key]); ok {
			windows = append(windows, window)
		}
	}
	if rateLimit, ok := quotaObject(quota["rate_limit"]); ok {
		for _, key := range []string{"primary_window", "secondary_window"} {
			if window, ok := quotaObject(rateLimit[key]); ok {
				windows = append(windows, window)
			}
		}
	}
	if rawWindows, ok := quota["windows"].([]any); ok {
		for _, rawWindow := range rawWindows {
			if window, ok := quotaObject(rawWindow); ok {
				windows = append(windows, window)
			}
		}
	}
	return windows
}

func maxProviderQuotaUsedPercent(windows []map[string]any) float64 {
	used := 0.0
	for _, window := range windows {
		if value, ok := quotaFloat64(window["used_percent"]); ok && value > used {
			used = value
		}
	}
	return used
}

func quotaObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func quotaString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func quotaBool(value any) (bool, bool) {
	boolean, ok := value.(bool)
	return boolean, ok
}

func quotaInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		if typed > 0 && typed == math.Trunc(typed) {
			return int64(typed)
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
	}
	return 0
}

func quotaFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case json.Number:
		if parsed, err := typed.Float64(); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			return parsed, true
		}
	}
	return 0, false
}

func providerObservationSuccess(statusCode int, errorCode string) bool {
	if statusCode >= 200 && statusCode < 400 {
		return true
	}
	if statusCode >= 400 && statusCode < 500 && statusCode != 429 {
		code := strings.ToLower(strings.TrimSpace(errorCode))
		return !strings.Contains(code, "auth") && !strings.Contains(code, "credential")
	}
	return false
}
