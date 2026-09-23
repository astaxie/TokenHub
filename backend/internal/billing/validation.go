package billing

import (
	"net/url"
	"strconv"
	"strings"
)

// ConnectorInput is the transport-independent input used to create a billing
// connector. Adapters may map HTTP, CLI, or another protocol into this shape.
type ConnectorInput struct {
	Name                    string
	Type                    string
	BaseURL                 string
	Status                  string
	ScheduleIntervalMinutes int
	Config                  map[string]string
	Credentials             map[string]string
}

// ConnectorPatchInput is the transport-independent partial update for a
// connector. A nil field is left unchanged.
type ConnectorPatchInput struct {
	Name                    *string
	BaseURL                 *string
	Status                  *string
	ScheduleIntervalMinutes *int
	Config                  map[string]string
	Credentials             map[string]string
}

// NormalizeConnector applies billing connector defaults and validates all
// provider-specific rules before persistence or synchronization.
func NormalizeConnector(input ConnectorInput) (Connector, error) {
	connector := Connector{
		Name:                    strings.TrimSpace(input.Name),
		Type:                    strings.ToLower(strings.TrimSpace(input.Type)),
		BaseURL:                 strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"),
		Status:                  strings.ToLower(strings.TrimSpace(input.Status)),
		ScheduleIntervalMinutes: input.ScheduleIntervalMinutes,
		Config:                  input.Config,
		Credentials:             input.Credentials,
	}
	if connector.Status == "" {
		connector.Status = StatusActive
	}
	if err := ValidateConnector(connector); err != nil {
		return Connector{}, err
	}
	return connector, nil
}

// NormalizeConnectorPatch applies a partial update to current and validates
// the resulting connector without changing persistence or transport concerns.
func NormalizeConnectorPatch(input ConnectorPatchInput, current Connector) (Connector, error) {
	patch := Connector{ScheduleIntervalMinutes: -1}
	candidate := current
	if input.Name != nil {
		patch.Name = strings.TrimSpace(*input.Name)
		candidate.Name = patch.Name
	}
	if input.BaseURL != nil {
		patch.BaseURL = strings.TrimRight(strings.TrimSpace(*input.BaseURL), "/")
		candidate.BaseURL = patch.BaseURL
	}
	if input.Status != nil {
		patch.Status = strings.ToLower(strings.TrimSpace(*input.Status))
		candidate.Status = patch.Status
	}
	if input.ScheduleIntervalMinutes != nil {
		patch.ScheduleIntervalMinutes = *input.ScheduleIntervalMinutes
		candidate.ScheduleIntervalMinutes = patch.ScheduleIntervalMinutes
	}
	if input.Config != nil {
		patch.Config = input.Config
		candidate.Config = input.Config
	}
	if input.Credentials != nil {
		patch.Credentials = input.Credentials
	}
	if err := ValidateConnector(candidate); err != nil {
		return Connector{}, err
	}
	return patch, nil
}

// ValidateConnector enforces billing policy independently of any transport or
// persistence adapter.
func ValidateConnector(connector Connector) error {
	if connector.Name == "" || connector.BaseURL == "" {
		return NewError(ErrorInvalidInput, "invalid_billing_connector", "name and base_url are required")
	}
	switch connector.Type {
	case ConnectorAliyun, ConnectorNewAPI, ConnectorOneAPI:
	default:
		return NewError(ErrorInvalidInput, "invalid_billing_connector_type", "type must be aliyun, newapi, or oneapi")
	}
	if connector.Status != StatusActive && connector.Status != StatusDisabled {
		return NewError(ErrorInvalidInput, "invalid_billing_connector_status", "status must be active or disabled")
	}
	if connector.ScheduleIntervalMinutes < 0 {
		return NewError(ErrorInvalidInput, "invalid_billing_schedule", "schedule_interval_minutes cannot be negative")
	}
	baseURL, err := url.Parse(connector.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return NewError(ErrorInvalidInput, "invalid_billing_base_url", "base_url must be an HTTP(S) URL without credentials, query parameters, or fragments")
	}
	allowed := allowedConfig(connector.Type)
	for key := range connector.Config {
		if _, ok := allowed[key]; !ok {
			return NewError(ErrorInvalidInput, "invalid_billing_config", "Billing connector config contains an unsupported field")
		}
	}
	if endpoint := strings.TrimSpace(connector.Config["endpoint"]); endpoint != "" {
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return NewError(ErrorInvalidInput, "invalid_billing_endpoint", "Billing connector endpoint is invalid")
		}
	}
	if connector.Type == ConnectorNewAPI {
		userID, parseErr := strconv.ParseInt(strings.TrimSpace(connector.Config["user_id"]), 10, 64)
		if parseErr != nil || userID <= 0 {
			return NewError(ErrorInvalidInput, "invalid_billing_config", "NewAPI user_id must be a positive integer")
		}
	}
	return nil
}

func allowedConfig(connectorType string) map[string]struct{} {
	allowed := map[string]struct{}{
		"currency": {}, "max_retries": {}, "page_size": {}, "provider_id": {},
		"provider_resource_id": {}, "rate_limit_per_second": {}, "retry_base_ms": {},
	}
	switch connectorType {
	case ConnectorAliyun:
		allowed["product_code"] = struct{}{}
		allowed["source_timezone"] = struct{}{}
	case ConnectorNewAPI:
		allowed["quota_per_unit"] = struct{}{}
		allowed["user_id"] = struct{}{}
	case ConnectorOneAPI:
		allowed["endpoint"] = struct{}{}
		allowed["quota_per_unit"] = struct{}{}
	}
	return allowed
}
