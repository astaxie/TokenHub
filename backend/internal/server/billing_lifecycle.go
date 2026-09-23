package server

import "time"

const pluginBackgroundJobSchedulerInterval = time.Minute

func (s *Server) StartBillingScheduler() {
	s.billing.StartScheduler(30 * time.Second)
	s.reconciliation.StartScheduler(30 * time.Second)
	s.credentialRefresh.StartScheduler(providerCredentialRefreshInterval)
	s.payloadRetention.StartScheduler(requestPayloadRetentionInterval)
	s.pluginBackgroundRunner.StartScheduler(pluginBackgroundJobSchedulerInterval)
}
