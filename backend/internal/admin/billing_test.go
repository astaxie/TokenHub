package admin

import (
	"net/http"
	"testing"

	"tokenhub/backend/internal/billing"
)

type testTransportError struct {
	status int
	code   string
}

func (e *testTransportError) Error() string { return e.code }

func testNewError(status int, code, _ string) error {
	return &testTransportError{status: status, code: code}
}

func TestBillingConnectorHTTPValidationAndResponseRedaction(t *testing.T) {
	_, err := connectorFromRequest(billingConnectorRequest{
		Name: "Unsafe", Type: billing.ConnectorOneAPI, BaseURL: "https://billing.example.test?token=secret",
	}, testNewError)
	var transportErr *testTransportError
	if !asTransportError(err, &transportErr) || transportErr.status != http.StatusBadRequest || transportErr.code != "invalid_billing_base_url" {
		t.Fatalf("credential-bearing base URL was accepted or misclassified: %v", err)
	}
	connector, err := connectorFromRequest(billingConnectorRequest{
		Name: "Finance", Type: billing.ConnectorOneAPI, BaseURL: "https://billing.example.test",
		Credentials: map[string]string{"api_token": "secret"},
	}, testNewError)
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

func asTransportError(err error, target **testTransportError) bool {
	value, ok := err.(*testTransportError)
	if ok {
		*target = value
	}
	return ok
}
