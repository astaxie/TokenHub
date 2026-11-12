package server

import (
	"context"
	"log"
	"sync"
)

// Collection is independent of inference health probes: an exhausted key still
// needs its reset and balance checked. Concurrency and per-call deadlines bound
// the work; cluster tasks and resource leases prevent duplicate refreshes.
func (s *Server) runProviderAllowanceRefresh(ctx context.Context) error {
	store, ok := s.store.(*GormStore)
	if !ok {
		return nil
	}
	semaphore := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for _, resource := range s.store.ListProviderResources() {
		if resource.Status != StatusActive {
			continue
		}
		provider, exists := s.store.GetProvider(resource.ProviderID)
		if !exists || provider.Status != StatusActive {
			continue
		}
		wait.Add(1)
		go func(resource ProviderResource, provider Provider) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			s.pluginRuntimeMu.RLock()
			defer s.pluginRuntimeMu.RUnlock()
			config, err := store.providerAllowanceConfig(ctx, resource.ID)
			if err != nil {
				return
			}
			if config.Source.Enabled {
				_, _, err = s.refreshConfiguredAllowance(ctx, resource, false)
			} else if d, known := s.adapterRegistry.Describe(provider.Type); known && adapterSupports(d, AdapterCapabilityQuota) {
				_, err = s.queryProviderMonitoringQuota(ctx, provider, resource)
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("[tokenhub] allowance refresh failed for %s: %s", resource.ID, AsHTTPError(err).Code)
			}
			if ctx.Err() != nil {
				return
			}
			s.refreshAllowanceEntitlements(ctx, resource, config)
		}(resource, provider)
	}
	wait.Wait()
	s.deliverAllowanceAlerts(ctx)
	return store.pruneProviderAllowanceHistory(ctx)
}
