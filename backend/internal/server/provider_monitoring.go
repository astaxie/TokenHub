package server

import (
	"context"
	"math"
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
	ResourceID   string              `json:"resource_id"`
	ResourceName string              `json:"resource_name"`
	Quota        *OpenAIAccountQuota `json:"quota,omitempty"`
	ErrorCode    string              `json:"error_code,omitempty"`
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
				quota, err := s.queryOpenAIAccountQuota(ctx, item.ID)
				account := ProviderQuotaAccountSummary{ResourceID: item.ID, ResourceName: item.Name}
				if err != nil {
					account.ErrorCode = AsHTTPError(err).Code
				} else {
					account.Quota = &quota
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
		if item.account.Quota == nil {
			summary.FailedAccounts++
			continue
		}
		summary.SuccessfulAccounts++
		mergeProviderQuotaSummary(summary, *item.account.Quota)
	}
	for index := range snapshots {
		sort.Slice(snapshots[index].Quota.Accounts, func(i, j int) bool {
			return snapshots[index].Quota.Accounts[i].ResourceName < snapshots[index].Quota.Accounts[j].ResourceName
		})
	}
}

func mergeProviderQuotaSummary(summary *ProviderQuotaSummary, quota OpenAIAccountQuota) {
	remaining := openAIQuotaRemainingPercent(quota)
	if summary.SuccessfulAccounts == 1 || remaining < summary.RemainingPercent {
		summary.RemainingPercent = remaining
		summary.PlanType = quota.PlanType
	}
	if quota.FetchedAt > summary.FetchedAt {
		summary.FetchedAt = quota.FetchedAt
	}
	if quota.RateLimit == nil {
		return
	}
	summary.LimitReached = summary.LimitReached || quota.RateLimit.LimitReached || !quota.RateLimit.Allowed
	for _, window := range []*OpenAIAccountQuotaWindow{quota.RateLimit.PrimaryWindow, quota.RateLimit.SecondaryWindow} {
		if window == nil || window.ResetAt <= 0 {
			continue
		}
		if summary.EarliestResetAt == 0 || window.ResetAt < summary.EarliestResetAt {
			summary.EarliestResetAt = window.ResetAt
		}
	}
}

func openAIQuotaRemainingPercent(quota OpenAIAccountQuota) float64 {
	if quota.RateLimit == nil {
		return 100
	}
	used := 0.0
	for _, window := range []*OpenAIAccountQuotaWindow{quota.RateLimit.PrimaryWindow, quota.RateLimit.SecondaryWindow} {
		if window != nil && window.UsedPercent > used {
			used = window.UsedPercent
		}
	}
	return math.Max(0, math.Min(100, 100-used))
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
