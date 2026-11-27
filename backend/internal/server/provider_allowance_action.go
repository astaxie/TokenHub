package server

import (
	"context"
	"time"
)

func (s *Server) recordQuotaActionFailure(ctx context.Context, pluginID, actionID string, opts providerPluginActionOptions, err error, started time.Time) {
	if !opts.ApplySideEffects {
		return
	}
	action, ok := s.pluginActions.Describe(pluginID, actionID)
	if !ok || action.Capability != "quota.read" {
		return
	}
	failure := AsHTTPError(pluginActionHTTPError(err))
	if failure.Status < 500 && failure.Status != 429 && failure.Code != "provider_resource_reauthorization_required" {
		return
	}
	if expected, ok := pluginActionResourceRevision(ctx); ok {
		s.recordProviderQuotaFailure(ctx, expected, err, started)
	}
}
