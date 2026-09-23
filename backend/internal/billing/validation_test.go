package billing

import "testing"

func TestNormalizeConnectorAppliesDefaultsAndCanonicalizesFields(t *testing.T) {
	connector, err := NormalizeConnector(ConnectorInput{
		Name: " Finance ", Type: " ONEAPI ", BaseURL: "https://billing.example.test/", Config: map[string]string{
			"endpoint": "api/log/self", "page_size": "10",
		},
	})
	if err != nil {
		t.Fatalf("normalize connector: %v", err)
	}
	if connector.Name != "Finance" || connector.Type != ConnectorOneAPI || connector.BaseURL != "https://billing.example.test" || connector.Status != StatusActive {
		t.Fatalf("unexpected normalized connector: %#v", connector)
	}
}

func TestValidateConnectorRejectsProviderPolicyViolations(t *testing.T) {
	tests := []struct {
		name string
		in   ConnectorInput
		code string
	}{
		{name: "unsupported type", in: ConnectorInput{Name: "x", Type: "stripe", BaseURL: "https://billing.example.test"}, code: "invalid_billing_connector_type"},
		{name: "credential-bearing URL", in: ConnectorInput{Name: "x", Type: ConnectorOneAPI, BaseURL: "https://user:secret@billing.example.test"}, code: "invalid_billing_base_url"},
		{name: "unsupported config", in: ConnectorInput{Name: "x", Type: ConnectorOneAPI, BaseURL: "https://billing.example.test", Config: map[string]string{"private_key": "secret"}}, code: "invalid_billing_config"},
		{name: "absolute endpoint", in: ConnectorInput{Name: "x", Type: ConnectorOneAPI, BaseURL: "https://billing.example.test", Config: map[string]string{"endpoint": "https://other.example.test"}}, code: "invalid_billing_endpoint"},
		{name: "missing NewAPI user", in: ConnectorInput{Name: "x", Type: ConnectorNewAPI, BaseURL: "https://billing.example.test"}, code: "invalid_billing_config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeConnector(test.in)
			if _, code, _, ok := ErrorInfo(err); !ok || code != test.code {
				t.Fatalf("error = %v, want code %q", err, test.code)
			}
		})
	}
}

func TestNormalizeConnectorPatchValidatesResultingConnector(t *testing.T) {
	current, err := NormalizeConnector(ConnectorInput{Name: "Finance", Type: ConnectorNewAPI, BaseURL: "https://billing.example.test", Config: map[string]string{"user_id": "42"}})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := NormalizeConnectorPatch(ConnectorPatchInput{Status: stringPointer("DISABLED")}, current)
	if err != nil {
		t.Fatalf("normalize patch: %v", err)
	}
	if patch.Status != StatusDisabled || patch.ScheduleIntervalMinutes != -1 {
		t.Fatalf("unexpected patch: %#v", patch)
	}
	_, err = NormalizeConnectorPatch(ConnectorPatchInput{Config: map[string]string{"user_id": "0"}}, current)
	if _, code, _, ok := ErrorInfo(err); !ok || code != "invalid_billing_config" {
		t.Fatalf("invalid patch error = %v", err)
	}
}

func stringPointer(value string) *string { return &value }
