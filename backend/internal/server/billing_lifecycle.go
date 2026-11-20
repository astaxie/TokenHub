package server

import "time"

const pluginBackgroundJobSchedulerInterval = time.Minute

func (s *Server) StartBillingScheduler() {
	s.overviewBackfillStart.Do(func() {
		go func() { defer close(s.overviewBackfillDone); s.runOverviewBackfill(s.overviewBackfillContext) }()
	})
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
