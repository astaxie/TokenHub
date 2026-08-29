package server

import (
	"context"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const (
	providerCredentialRefreshDueJobCapability = "credentials.refresh_due"
	providerQuotaRefreshDueJobCapability      = "quota.refresh_due"
)

func registerBuiltinPluginBackgroundJobs(server *Server) {
	mustRegisterPluginBackgroundJob(server.pluginBackgroundJobs, pluginmeta.BackgroundJobDescriptor{
		PluginID:       "tokenhub.provider.openai-codex",
		JobID:          "openai_codex.credentials.refresh_due",
		Title:          "Refresh due OpenAI Codex account credentials",
		Capability:     providerCredentialRefreshDueJobCapability,
		Subject:        ProviderOpenAICodex,
		Schedule:       "1m",
		TimeoutMillis:  120000,
		MaxConcurrency: 1,
		OutputSchema:   backgroundJobCountSchema(),
	}, pluginmeta.BackgroundJobHandlerFunc(func(ctx context.Context, _ pluginmeta.BackgroundJobInvocation) (pluginmeta.BackgroundJobResult, error) {
		return pluginmeta.BackgroundJobResult{Data: server.refreshDueProviderCredentialsWithPluginJob(ctx, ProviderOpenAICodex)}, nil
	}))
	mustRegisterPluginBackgroundJob(server.pluginBackgroundJobs, pluginmeta.BackgroundJobDescriptor{
		PluginID:       "tokenhub.provider.openai-codex",
		JobID:          "openai_codex.quota.refresh_due",
		Title:          "Refresh due OpenAI Codex account quotas",
		Capability:     providerQuotaRefreshDueJobCapability,
		Subject:        ProviderOpenAICodex,
		Schedule:       "10m",
		TimeoutMillis:  120000,
		MaxConcurrency: 1,
		OutputSchema:   backgroundJobCountSchema(),
	}, pluginmeta.BackgroundJobHandlerFunc(func(ctx context.Context, _ pluginmeta.BackgroundJobInvocation) (pluginmeta.BackgroundJobResult, error) {
		return pluginmeta.BackgroundJobResult{Data: server.refreshDueProviderQuotasWithPluginJob(ctx, ProviderOpenAICodex)}, nil
	}))
}

func (s *Server) providerCredentialRefreshBackgroundJobRegistered(providerType string) bool {
	_, ok := s.providerPluginBackgroundJobDescriptor(providerType, AdapterCapabilityOAuth, providerCredentialRefreshDueJobCapability)
	return ok
}

func (s *Server) providerPluginBackgroundJobDescriptor(providerType string, capability AdapterCapability, jobCapability string) (pluginmeta.BackgroundJobDescriptor, bool) {
	descriptor, ok := s.adapterRegistry.Describe(providerType)
	if !ok || descriptor.PluginID == "" || !adapterSupports(descriptor, capability) {
		return pluginmeta.BackgroundJobDescriptor{}, false
	}
	for _, job := range s.pluginBackgroundJobs.List() {
		if job.PluginID != descriptor.PluginID || job.Capability != jobCapability {
			continue
		}
		if job.Subject != "" && job.Subject != providerType {
			continue
		}
		return job, true
	}
	return pluginmeta.BackgroundJobDescriptor{}, false
}

func (s *Server) refreshDueProviderCredentialsWithPluginJob(ctx context.Context, providerType string) map[string]int {
	result := map[string]int{"refreshed": 0, "failed": 0, "skipped": 0}
	providersByID := s.providersByID()
	for _, resource := range s.store.ListProviderResources() {
		if ctx.Err() != nil {
			return result
		}
		resourceProviderType, due := providerResourceCredentialRefreshProviderType(s.store, providersByID, resource)
		if !due || resourceProviderType != providerType {
			continue
		}
		handled, err := s.refreshProviderResourceCredentialsWithPluginAction(ctx, resource)
		if err != nil {
			result["failed"]++
			continue
		}
		if handled {
			result["refreshed"]++
		} else {
			result["skipped"]++
		}
	}
	return result
}

func (s *Server) refreshDueProviderQuotasWithPluginJob(ctx context.Context, providerType string) map[string]int {
	result := map[string]int{"refreshed": 0, "failed": 0, "skipped": 0}
	providersByID := s.providersByID()
	for _, resource := range s.store.ListProviderResources() {
		if ctx.Err() != nil {
			return result
		}
		provider, ok := providersByID[resource.ProviderID]
		if !ok || provider.Type != providerType || resource.Status != StatusActive || !s.store.IsProviderAccountResourceType(provider.Type, resource.ResourceType) {
			continue
		}
		payload := map[string]any{"resource_id": resource.ID, "refresh": true}
		_, handled, err := s.executeProviderCapabilityAction(ctx, AdminUser{ID: "system", Name: "System", Role: "system"}, provider.Type, AdapterCapabilityQuota, "quota.read", payload, providerPluginActionOptions{
			ApplySideEffects: true,
			ResourceType:     resource.ResourceType,
		})
		if err != nil {
			result["failed"]++
			continue
		}
		if handled {
			result["refreshed"]++
		} else {
			result["skipped"]++
		}
	}
	return result
}

func (s *Server) providersByID() map[string]Provider {
	providersByID := map[string]Provider{}
	for _, provider := range s.store.ListProviders() {
		providersByID[provider.ID] = provider
	}
	return providersByID
}

func backgroundJobCountSchema() map[string]any {
	return actionObjectSchema([]string{"refreshed", "failed", "skipped"}, map[string]string{
		"refreshed": "integer",
		"failed":    "integer",
		"skipped":   "integer",
	})
}

func mustRegisterPluginBackgroundJob(jobs *pluginmeta.BackgroundJobBroker, descriptor pluginmeta.BackgroundJobDescriptor, handler pluginmeta.BackgroundJobHandler) {
	if err := jobs.Register(descriptor, handler); err != nil {
		panic(err)
	}
}
