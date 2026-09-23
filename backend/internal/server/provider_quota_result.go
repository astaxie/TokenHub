package server

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

type providerQuotaResetCredits struct {
	AvailableCount int
	Data           map[string]any
}

func providerQuotaResetCreditsFromOpenAI(details openAIAccountQuotaResetCredits) providerQuotaResetCredits {
	data, ok := providerQuotaResetObjectFromData(details)
	if !ok {
		data = map[string]any{"available_count": details.AvailableCount}
	}
	return providerQuotaResetCredits{AvailableCount: details.AvailableCount, Data: data}
}

func providerQuotaResetCreditsFromActionData(data any) (providerQuotaResetCredits, bool) {
	if result, ok := data.(providerQuotaResetCredits); ok {
		return result, result.AvailableCount >= 0 && len(result.Data) > 0
	}
	payload, ok := providerQuotaResetObjectFromData(data)
	if !ok {
		return providerQuotaResetCredits{}, false
	}
	availableCount, ok := providerQuotaResetNonNegativeInt(payload["available_count"])
	if !ok {
		return providerQuotaResetCredits{}, false
	}
	if _, ok := payload["credits"].([]any); !ok {
		return providerQuotaResetCredits{}, false
	}
	return providerQuotaResetCredits{AvailableCount: availableCount, Data: payload}, true
}

func (result providerQuotaResetCredits) MarshalJSON() ([]byte, error) {
	return json.Marshal(result.normalizedData())
}

func (result providerQuotaResetCredits) normalizedData() map[string]any {
	data := providerQuotaResetCopyMap(result.Data)
	if data == nil {
		data = map[string]any{}
	}
	data["available_count"] = result.AvailableCount
	return data
}

type providerQuotaResetResult struct {
	Code         string
	Status       string
	OperationID  string
	WindowsReset *int
	Data         map[string]any
}

func providerQuotaResetResultFromOpenAI(result openAIAccountQuotaResetResult) providerQuotaResetResult {
	data, ok := providerQuotaResetObjectFromData(result)
	if !ok {
		data = map[string]any{
			"code":          result.Code,
			"windows_reset": result.WindowsReset,
		}
	}
	windowsReset := result.WindowsReset
	return providerQuotaResetResult{
		Code:         strings.TrimSpace(result.Code),
		WindowsReset: &windowsReset,
		Data:         data,
	}
}

func providerQuotaResetResultFromActionData(data any) (providerQuotaResetResult, bool) {
	if result, ok := data.(providerQuotaResetResult); ok {
		return result, providerQuotaResetResultIsValid(result)
	}
	payload, ok := providerQuotaResetObjectFromData(data)
	if !ok || len(payload) == 0 {
		return providerQuotaResetResult{}, false
	}
	result := providerQuotaResetResult{
		Code:        providerQuotaResetString(payload["code"]),
		Status:      providerQuotaResetString(payload["status"]),
		OperationID: providerQuotaResetString(payload["operation_id"]),
		Data:        payload,
	}
	if value, exists := payload["windows_reset"]; exists {
		windowsReset, ok := providerQuotaResetNonNegativeInt(value)
		if !ok {
			return providerQuotaResetResult{}, false
		}
		result.WindowsReset = &windowsReset
	}
	return result, providerQuotaResetResultIsValid(result)
}

func providerQuotaResetResultIsValid(result providerQuotaResetResult) bool {
	if result.WindowsReset != nil && *result.WindowsReset < 0 {
		return false
	}
	return strings.TrimSpace(result.Code) != "" ||
		strings.TrimSpace(result.Status) != "" ||
		strings.TrimSpace(result.OperationID) != "" ||
		strings.TrimSpace(providerQuotaResetString(result.Data["message"])) != ""
}

func (result providerQuotaResetResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(result.normalizedData())
}

func (result providerQuotaResetResult) normalizedData() map[string]any {
	data := providerQuotaResetCopyMap(result.Data)
	if data == nil {
		data = map[string]any{}
	}
	if result.Code != "" {
		data["code"] = result.Code
	}
	if result.Status != "" {
		data["status"] = result.Status
	}
	if result.OperationID != "" {
		data["operation_id"] = result.OperationID
	}
	if result.WindowsReset != nil {
		data["windows_reset"] = *result.WindowsReset
	}
	return data
}

func providerQuotaResetResultAudit(result providerQuotaResetResult) map[string]any {
	data := map[string]any{}
	if result.Code != "" {
		data["code"] = result.Code
	}
	if result.Status != "" {
		data["status"] = result.Status
	}
	if result.OperationID != "" {
		data["operation_id"] = result.OperationID
	}
	if result.WindowsReset != nil {
		data["windows_reset"] = *result.WindowsReset
	}
	if len(data) == 0 {
		data["result"] = "accepted"
	}
	return data
}

func providerQuotaResetObjectFromData(data any) (map[string]any, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, false
	}
	return payload, true
}

func providerQuotaResetCopyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func providerQuotaResetString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func providerQuotaResetNonNegativeInt(value any) (int, bool) {
	var number int64
	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int64:
		number = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) {
			return 0, false
		}
		number = int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if number < 0 || int64(int(number)) != number {
		return 0, false
	}
	return int(number), true
}
