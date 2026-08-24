package server

import (
	"context"
	"log"
	"sync"
	"time"
)

const providerCredentialRefreshInterval = time.Minute
const providerCredentialRefreshConcurrency = 4

// ProviderCredentialRefreshService renews OAuth access tokens shortly before
// they expire. RefreshProviderResourceCredentials owns the cluster lease, so
// every replica may run this scheduler without rotating a token concurrently.
type ProviderCredentialRefreshService struct {
	store Store

	schedulerOnce sync.Once
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newProviderCredentialRefreshService(store Store) *ProviderCredentialRefreshService {
	return &ProviderCredentialRefreshService{store: store}
}

func (s *ProviderCredentialRefreshService) RunDue(ctx context.Context) {
	resources := make([]ProviderResource, 0)
	for _, resource := range s.store.ListProviderResources() {
		if ctx.Err() != nil {
			return
		}
		if !isOpenAIAccountResource(resource.ResourceType) || resource.Status != StatusActive || resource.CredentialSummary["has_refresh_token"] != "true" || resource.CredentialSummary[openAIAccountReauthorizationRequiredOption] == "true" {
			continue
		}
		resources = append(resources, resource)
	}
	if len(resources) == 0 {
		return
	}
	workerCount := min(providerCredentialRefreshConcurrency, len(resources))
	jobs := make(chan ProviderResource)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for resource := range jobs {
				s.renewResource(ctx, resource)
			}
		}()
	}
	for _, resource := range resources {
		select {
		case jobs <- resource:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *ProviderCredentialRefreshService) renewResource(ctx context.Context, resource ProviderResource) {
	if _, err := s.store.RefreshProviderResourceCredentials(ctx, resource.ID, false); err != nil {
		code := "credential_refresh_failed"
		if httpErr := AsHTTPError(err); httpErr != nil && httpErr.Code != "" {
			code = httpErr.Code
		}
		log.Printf("[tokenhub] OAuth token renewal failed for provider resource %s: code=%s", resource.ID, code)
	}
}

func (s *ProviderCredentialRefreshService) StartScheduler(interval time.Duration) {
	if interval <= 0 {
		interval = providerCredentialRefreshInterval
	}
	s.schedulerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.schedulerStop = cancel
		s.schedulerDone = make(chan struct{})
		go func() {
			defer close(s.schedulerDone)
			s.RunDue(ctx)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.RunDue(ctx)
				}
			}
		}()
	})
}

func (s *ProviderCredentialRefreshService) Shutdown(ctx context.Context) error {
	if s.schedulerStop == nil {
		return nil
	}
	s.schedulerStop()
	select {
	case <-s.schedulerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
