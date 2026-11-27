package server

import "encoding/json"

func (r *OpenAIAccountRateLimit) UnmarshalJSON(data []byte) error {
	type rateLimit OpenAIAccountRateLimit
	var value rateLimit
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = OpenAIAccountRateLimit(value)
	allowed, ok := fields["allowed"]
	r.allowedMissing = !ok || string(allowed) == "null"
	return nil
}

func (r OpenAIAccountRateLimit) MarshalJSON() ([]byte, error) {
	type rateLimit OpenAIAccountRateLimit
	encoded, err := json.Marshal(rateLimit(r))
	if err != nil || !r.allowedMissing {
		return encoded, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	delete(fields, "allowed")
	return json.Marshal(fields)
}

// Preserve the distinction between an explicit zero and a field the provider
// omitted. Programmatically constructed windows retain their existing encoding.
func (w *OpenAIAccountQuotaWindow) UnmarshalJSON(data []byte) error {
	type window OpenAIAccountQuotaWindow
	var value window
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*w = OpenAIAccountQuotaWindow(value)
	used, ok := fields["used_percent"]
	w.usedPercentMissing = !ok || string(used) == "null"
	return nil
}

func (w OpenAIAccountQuotaWindow) MarshalJSON() ([]byte, error) {
	type window OpenAIAccountQuotaWindow
	encoded, err := json.Marshal(window(w))
	if err != nil || !w.usedPercentMissing {
		return encoded, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	delete(fields, "used_percent")
	return json.Marshal(fields)
}
