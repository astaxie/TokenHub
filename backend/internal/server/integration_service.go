package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type IntegrationService struct {
	store    Store
	registry *AdapterRegistry
}

type ProviderProbeBatchResult struct {
	ProviderID string                `json:"provider_id"`
	Healthy    bool                  `json:"healthy"`
	Succeeded  int                   `json:"succeeded"`
	Failed     int                   `json:"failed"`
	Results    []ProviderProbeResult `json:"results,omitempty"`
	Errors     []string              `json:"errors,omitempty"`
}

func NewIntegrationService(store Store, registry *AdapterRegistry) *IntegrationService {
	return &IntegrationService{store: store, registry: registry}
}

func (s *IntegrationService) TestProviderResource(ctx context.Context, resourceID string, request *ProviderProbeRequest) (any, error) {
	resource, ok := integrationProviderResource(s.store, resourceID)
	if !ok {
		return nil, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := integrationProvider(s.store, resource.ProviderID)
	if !ok {
		return nil, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	adapter, err := s.registry.Resolve(provider.Type)
	if err != nil {
		return nil, err
	}
	prober, supported := adapter.(ProviderResourceProber)
	if !supported {
		return s.store.TestProviderResource(resourceID)
	}
	probeRequest := prober.DefaultProbeRequest()
	if request != nil {
		probeRequest = *request
	}
	startedAt := time.Now()
	result, err := prober.Probe(ctx, provider, resource, probeRequest)
	s.finishProbe(provider, resource, startedAt, err, result.Usage)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *IntegrationService) TestProvider(ctx context.Context, providerID string) (any, error) {
	provider, ok := integrationProvider(s.store, providerID)
	if !ok {
		return nil, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	adapter, err := s.registry.Resolve(provider.Type)
	if err != nil {
		return nil, err
	}
	if _, supported := adapter.(ProviderResourceProber); !supported {
		return s.store.TestProvider(providerID)
	}
	result := ProviderProbeBatchResult{ProviderID: providerID}
	var firstErr error
	for _, resource := range s.store.ListProviderResources() {
		if resource.ProviderID != providerID || resource.Status != StatusActive {
			continue
		}
		probe, probeErr := s.TestProviderResource(ctx, resource.ID, nil)
		if probeErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, AsHTTPError(probeErr).Code)
			if firstErr == nil {
				firstErr = probeErr
			}
			continue
		}
		if typed, ok := probe.(ProviderProbeResult); ok {
			result.Results = append(result.Results, typed)
		}
		result.Succeeded++
	}
	result.Healthy = result.Succeeded > 0
	if result.Succeeded == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, NewHTTPError(http.StatusConflict, "provider_resource_required", "Provider has no active account resource to test")
	}
	_, _ = s.store.SetProviderHealth(providerID, true)
	return result, nil
}

func (s *IntegrationService) finishProbe(provider Provider, resource ProviderResource, startedAt time.Time, err error, usage Usage) {
	disposition := providerErrorDisposition(err)
	if err == nil {
		s.store.FinishProviderResourceAttempt(resource.ID, "", true, usage)
	} else if disposition == ProviderErrorQuotaExhausted || disposition == ProviderErrorAuthBroken || disposition == ProviderErrorResourceBroken {
		s.store.FinishProviderResourceAttempt(resource.ID, "", false, usage)
	}
	_, code := statusAndCode(err)
	s.store.RecordProviderObservation(ProviderObservation{
		ProviderID:  provider.ID,
		ResourceID:  resource.ID,
		AdapterType: provider.Type,
		Source:      "active_probe",
		Operation:   "responses",
		Success:     err == nil,
		LatencyMS:   time.Since(startedAt).Milliseconds(),
		ErrorCode:   strings.TrimSpace(code),
	})
}

func integrationProvider(store Store, providerID string) (Provider, bool) {
	for _, provider := range store.ListProviders() {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return Provider{}, false
}

func integrationProviderResource(store Store, resourceID string) (ProviderResource, bool) {
	for _, resource := range store.ListProviderResources() {
		if resource.ID == resourceID {
			return resource, true
		}
	}
	return ProviderResource{}, false
}
