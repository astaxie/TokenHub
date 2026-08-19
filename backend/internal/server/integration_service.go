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
	client   *http.Client
}

type ProviderProbeBatchResult struct {
	ProviderID string                `json:"provider_id"`
	Healthy    bool                  `json:"healthy"`
	Succeeded  int                   `json:"succeeded"`
	Failed     int                   `json:"failed"`
	Results    []ProviderProbeResult `json:"results,omitempty"`
	Errors     []string              `json:"errors,omitempty"`
}

func NewIntegrationService(store Store, registry *AdapterRegistry, clients ...*http.Client) *IntegrationService {
	client := http.DefaultClient
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &IntegrationService{store: store, registry: registry, client: client}
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
	if provider.Type == ProviderKronk {
		kronk, ok := adapter.(KronkAdapter)
		if !ok {
			return nil, NewHTTPError(http.StatusInternalServerError, "provider_adapter_missing", "Kronk adapter is unavailable")
		}
		effective := effectiveProviderResourceConfig(provider, &resource)
		startedAt := time.Now()
		result, healthErr := kronk.Health(ctx, effective)
		s.finishProbe(ctx, provider, resource, startedAt, healthErr, Usage{})
		if healthErr != nil {
			return nil, healthErr
		}
		if _, recoverErr := s.store.RecoverProviderResource(resource.ID); recoverErr != nil {
			return nil, recoverErr
		}
		return result, nil
	}
	prober, supported := adapter.(ProviderResourceProber)
	if !supported {
		if provider.Type == ProviderMock {
			return s.store.TestProviderResource(resourceID)
		}
		effective := effectiveProviderResourceConfig(provider, &resource)
		startedAt := time.Now()
		_, probeErr := CustomProviderCatalogFromUpstream(ctx, s.client, ProviderCreateRequest{
			Type: effective.Type, BaseURL: effective.BaseURL, APIKey: effective.APIKey,
			Headers: effective.Headers, SensitiveHeaders: effective.SensitiveHeaders, Options: effective.Options,
		})
		s.finishProbe(ctx, provider, resource, startedAt, probeErr, Usage{})
		if probeErr != nil {
			return nil, probeErr
		}
		return s.store.RecoverProviderResource(resourceID)
	}
	probeRequest := prober.DefaultProbeRequest()
	if request != nil {
		probeRequest = *request
	}
	startedAt := time.Now()
	result, err := prober.Probe(ctx, provider, resource, probeRequest)
	s.finishProbe(ctx, provider, resource, startedAt, err, result.Usage)
	if err != nil {
		return nil, err
	}
	// An adapter probe that reached the upstream and came back clean is strong
	// enough to clear the breaker. The catalog-discovery fallback performs the
	// same upstream check and recovers its resource before returning above.
	if _, recoverErr := s.store.RecoverProviderResource(resource.ID); recoverErr != nil {
		return nil, recoverErr
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
	if provider.Type == ProviderKronk {
		kronk, ok := adapter.(KronkAdapter)
		if !ok {
			return nil, NewHTTPError(http.StatusInternalServerError, "provider_adapter_missing", "Kronk adapter is unavailable")
		}
		result, healthErr := kronk.Health(ctx, effectiveProviderResourceConfig(provider, nil))
		_, _ = s.store.SetProviderHealth(providerID, healthErr == nil)
		if healthErr != nil {
			return nil, healthErr
		}
		return result, nil
	}
	if _, supported := adapter.(ProviderResourceProber); !supported {
		if provider.Type == ProviderMock {
			return s.store.TestProvider(providerID)
		}
		effectiveProvider := effectiveProviderResourceConfig(provider, nil)
		var firstResourceErr error
		for _, resource := range s.store.ListProviderResources() {
			if resource.ProviderID == providerID && resource.Status == StatusActive {
				result, probeErr := s.TestProviderResource(ctx, resource.ID, nil)
				if probeErr != nil {
					if firstResourceErr == nil {
						firstResourceErr = probeErr
					}
					continue
				}
				_, _ = s.store.SetProviderHealth(providerID, true)
				return result, nil
			}
		}
		if firstResourceErr != nil {
			_, _ = s.store.SetProviderHealth(providerID, false)
			return nil, firstResourceErr
		}
		_, probeErr := CustomProviderCatalogFromUpstream(ctx, s.client, ProviderCreateRequest{
			Type: effectiveProvider.Type, BaseURL: effectiveProvider.BaseURL, APIKey: effectiveProvider.APIKey,
			Headers: effectiveProvider.Headers, SensitiveHeaders: effectiveProvider.SensitiveHeaders, Options: effectiveProvider.Options,
		})
		if probeErr != nil {
			_, _ = s.store.SetProviderHealth(providerID, false)
			return nil, probeErr
		}
		return s.store.SetProviderHealth(providerID, true)
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

func (s *IntegrationService) finishProbe(ctx context.Context, provider Provider, resource ProviderResource, startedAt time.Time, err error, usage Usage) {
	disposition := providerErrorDisposition(err)
	if err == nil {
		s.store.FinishProviderResourceAttempt(ctx, resource.ID, "", AttemptSucceeded, usage)
	} else if disposition == ProviderErrorQuotaExhausted || disposition == ProviderErrorAuthBroken || disposition == ProviderErrorResourceBroken {
		s.store.FinishProviderResourceAttempt(ctx, resource.ID, "", AttemptFailed, usage)
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
	return store.GetProvider(providerID)
}

func integrationProviderResource(store Store, resourceID string) (ProviderResource, bool) {
	return store.GetProviderResource(resourceID)
}
