package reconciliation_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/reconciliation"
)

func TestRunAuditSnapshotPreservesBehavioralFieldsWithoutSecrets(t *testing.T) {
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	snapshot := reconciliation.RunAuditSnapshot(reconciliation.Run{
		ID: "run-1", RuleID: "rule-1", ConnectorID: "connector-1", ConnectorType: "oneapi",
		ProviderID: "provider-1", ProviderResourceID: "resource-secret", Trigger: "scheduled",
		Status: reconciliation.RunSucceeded, RuleVersion: 2, RuleHash: "sha256:rule", InputHash: "sha256:input",
		PeriodStart: now.Add(-time.Hour), PeriodEnd: now, MatchedCount: 3, ErrorMessage: "internal details",
	})
	for _, key := range []string{"connector_id", "connector_type", "provider_scope_configured", "trigger", "rule_version", "rule_hash", "input_hash", "period_start", "period_end", "matched_count"} {
		if _, ok := snapshot[key]; !ok {
			t.Fatalf("audit snapshot lost %q: %#v", key, snapshot)
		}
	}
	if snapshot["provider_resource_scope_configured"] != true {
		t.Fatalf("audit snapshot lost scope state: %#v", snapshot)
	}
	encodedBytes, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal audit snapshot: %v", err)
	}
	encoded := string(encodedBytes)
	if strings.Contains(encoded, "resource-secret") || strings.Contains(encoded, "internal details") {
		t.Fatalf("audit snapshot exposed sensitive data: %s", encoded)
	}
	if _, ok := snapshot["provider_resource_id"]; ok {
		t.Fatalf("audit snapshot exposed provider resource ID: %#v", snapshot)
	}
	if _, ok := snapshot["error_message"]; ok {
		t.Fatalf("audit snapshot exposed error message: %#v", snapshot)
	}
}
