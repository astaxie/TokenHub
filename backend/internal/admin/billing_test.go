package admin

import (
	"testing"

	"tokenhub/backend/internal/billing"
)

func TestBillingConnectorHTTPValidationAndResponseRedaction(t *testing.T) {
	_, err := billing.NormalizeConnector(billing.ConnectorInput{
		Name: "Unsafe", Type: billing.ConnectorOneAPI, BaseURL: "https://billing.example.test?token=secret",
	})
	if kind, code, _, ok := billing.ErrorInfo(err); !ok || kind != billing.ErrorInvalidInput || code != "invalid_billing_base_url" {
		t.Fatalf("credential-bearing base URL was accepted or misclassified: %v", err)
	}
	connector, err := billing.NormalizeConnector(billing.ConnectorInput{
		Name: "Finance", Type: billing.ConnectorOneAPI, BaseURL: "https://billing.example.test",
		Credentials: map[string]string{"api_token": "secret"},
	})
	if err != nil {
		t.Fatalf("valid connector rejected: %v", err)
	}
	response := connectorResponse(billing.Connector{
		ID: "bcon_1", Name: connector.Name, Type: connector.Type, BaseURL: connector.BaseURL,
		CredentialsConfigured: true, CredentialFields: []string{"api_token"}, Credentials: connector.Credentials,
	})
	if response.CredentialsConfigured != true || len(response.CredentialFields) != 1 || response.CredentialFields[0] != "api_token" {
		t.Fatalf("credential summary changed: %#v", response)
	}
}
