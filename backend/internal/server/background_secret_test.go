package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackgroundJobAuditSnapshotRedactsSecrets(t *testing.T) {
	snapshot := backgroundJobAuditSnapshot(json.RawMessage(`{"resource_id":"rsrc_1","refresh_token":"request-refresh-secret","nested":{"access_token":"nested-secret"}}`))
	if strings.Contains(snapshot, "request-refresh-secret") || strings.Contains(snapshot, "nested-secret") {
		t.Fatalf("background job audit snapshot leaked secrets: %s", snapshot)
	}
	for _, expected := range []string{`"resource_id":"rsrc_1"`, `"refresh_token":"[redacted]"`, `"access_token":"[redacted]"`} {
		if !strings.Contains(snapshot, expected) {
			t.Fatalf("background job audit snapshot missing %s: %s", expected, snapshot)
		}
	}
}

func TestBackgroundJobAuditSnapshotHandlesEmptyPayload(t *testing.T) {
	if snapshot := backgroundJobAuditSnapshot(nil); snapshot != "" {
		t.Fatalf("empty background job payload snapshot = %q, want empty", snapshot)
	}
}

func TestBackgroundJobRedactErrorTextUsesPayloadAndKeywords(t *testing.T) {
	redacted := backgroundJobRedactErrorText(json.RawMessage(`{"refresh_token":"request-refresh-secret","resource_id":"rsrc_1"}`), "failed to refresh request-refresh-secret because the token expired")
	if strings.Contains(redacted, "request-refresh-secret") || strings.Contains(redacted, "token expired") {
		t.Fatalf("background job error leaked secrets or sensitive keyword text: %s", redacted)
	}
	if redacted != "[redacted]" {
		t.Fatalf("background job error = %q, want redacted", redacted)
	}
}
