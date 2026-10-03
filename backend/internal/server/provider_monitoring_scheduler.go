package server

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	providerMonitoringSchedulerInterval = 5 * time.Minute
	providerMonitoringTaskName          = "provider-monitoring"
	providerMonitoringProbeTimeout      = 2 * time.Minute
	providerMonitoringCheckConcurrency  = 4
)

type providerMonitoringScheduler struct {
	store Store
	run   func(context.Context) error

	schedulerMu   sync.Mutex
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newProviderMonitoringScheduler(store Store, run func(context.Context) error) *providerMonitoringScheduler {
	return &providerMonitoringScheduler{store: store, run: run}
}

func (s *providerMonitoringScheduler) RunDue(ctx context.Context, now time.Time) error {
	if s == nil || s.store == nil || s.run == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	revision := now.UTC().Truncate(providerMonitoringSchedulerInterval).Unix()
	return s.store.RunClusterTask(ctx, providerMonitoringTaskName, revision, s.run)
}

func (s *providerMonitoringScheduler) StartScheduler(interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		interval = providerMonitoringSchedulerInterval
	}
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.schedulerStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.schedulerStop = cancel
	s.schedulerDone = make(chan struct{})
	go func() {
		defer close(s.schedulerDone)
		s.runScheduled(ctx, time.Now().UTC())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.runScheduled(ctx, now.UTC())
			}
		}
	}()
}

func (s *providerMonitoringScheduler) runScheduled(ctx context.Context, now time.Time) {
	if err := s.RunDue(ctx, now); err != nil && ctx.Err() == nil {
		log.Printf("[tokenhub] provider monitoring scheduler failed: %v", err)
	}
}

func (s *providerMonitoringScheduler) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.schedulerMu.Lock()
	stop := s.schedulerStop
	done := s.schedulerDone
	s.schedulerMu.Unlock()
	if stop == nil {
		return nil
	}
	stop()
	select {
	case <-done:
		s.schedulerMu.Lock()
		if s.schedulerDone == done {
			s.schedulerStop = nil
			s.schedulerDone = nil
		}
		s.schedulerMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) runProviderMonitoring(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.pluginRuntimeMu.RLock()
	defer s.pluginRuntimeMu.RUnlock()
	if s.integrations == nil {
		return nil
	}
	providers := s.store.ListProviders()
	semaphore := make(chan struct{}, providerMonitoringCheckConcurrency)
	var wait sync.WaitGroup
	for _, provider := range providers {
		if provider.Status != StatusActive {
			continue
		}
		provider := provider
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			s.checkProviderMonitoring(ctx, provider)
		}()
	}
	wait.Wait()
	return nil
}

func (s *Server) checkProviderMonitoring(ctx context.Context, provider Provider) {
	probeCtx, cancel := context.WithTimeout(ctx, providerMonitoringProbeTimeout)
	defer cancel()
	startedAt := time.Now()
	_, probeErr := s.integrations.TestProvider(probeCtx, provider.ID)
	_, _ = s.store.SetProviderHealth(provider.ID, probeErr == nil)
	_, errorCode := statusAndCode(probeErr)
	s.store.RecordProviderObservation(ProviderObservation{
		ProviderID:  provider.ID,
		AdapterType: provider.Type,
		Source:      "active_probe",
		Operation:   "responses",
		Success:     probeErr == nil,
		LatencyMS:   time.Since(startedAt).Milliseconds(),
		ErrorCode:   errorCode,
	})
	if probeErr != nil && probeCtx.Err() == nil {
		log.Printf("[tokenhub] provider monitoring probe failed for %s: %s", provider.ID, errorCode)
	}

	descriptor, described := s.adapterRegistry.Describe(provider.Type)
	if !described || !adapterSupports(descriptor, AdapterCapabilityQuota) {
		return
	}
	for _, resource := range s.store.ListProviderResources() {
		if probeCtx.Err() != nil {
			return
		}
		if resource.ProviderID != provider.ID || resource.Status != StatusActive {
			continue
		}
		if _, err := s.queryProviderMonitoringQuota(probeCtx, provider, resource); err != nil && probeCtx.Err() == nil {
			_, code := statusAndCode(err)
			log.Printf("[tokenhub] provider monitoring quota refresh failed for %s: %s", resource.ID, code)
		}
	}
}
