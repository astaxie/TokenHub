package reconciliation

// RunAuditSnapshot returns the stable, redacted reconciliation run projection
// used by administrative and scheduled audit adapters.
func RunAuditSnapshot(run Run) map[string]any {
	return map[string]any{
		"id": run.ID, "rule_id": run.RuleID, "connector_id": run.ConnectorID,
		"connector_type": run.ConnectorType, "provider_scope_configured": run.ProviderID != "",
		"provider_resource_scope_configured": run.ProviderResourceID != "", "status": run.Status,
		"trigger": run.Trigger, "rule_version": run.RuleVersion, "rule_hash": run.RuleHash,
		"input_hash": run.InputHash, "period_start": run.PeriodStart, "period_end": run.PeriodEnd,
		"matched_count": run.MatchedCount, "provider_only_count": run.ProviderOnlyCount,
		"tokenhub_only_count": run.TokenHubOnlyCount, "amount_mismatch_count": run.AmountMismatchCount,
		"error_code": run.ErrorCode, "started_at": run.StartedAt, "finished_at": run.FinishedAt,
		"locked_at": run.LockedAt,
	}
}
