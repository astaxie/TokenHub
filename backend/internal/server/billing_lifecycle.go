package server

import "time"

const pluginBackgroundJobSchedulerInterval = time.Minute

func (s *Server) StartBillingScheduler() {
	s.billing.StartScheduler(30 * time.Second)
	s.reconciliation.StartScheduler(30 * time.Second)
	s.credentialRefresh.StartScheduler(providerCredentialRefreshInterval)
	s.payloadRetention.StartScheduler(requestPayloadRetentionInterval)
	if s.providerMonitoring != nil {
		s.providerMonitoring.StartScheduler(providerMonitoringSchedulerInterval)
	}
	if s.providerCatalogSync != nil && s.config.ProviderCatalogSyncIntervalSeconds > 0 {
		s.providerCatalogSync.StartScheduler(time.Duration(s.config.ProviderCatalogSyncIntervalSeconds) * time.Second)
	}
	s.pluginBackgroundRunner.StartScheduler(pluginBackgroundJobSchedulerInterval)
}
