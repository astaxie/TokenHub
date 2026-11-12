package allowance

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// Normalize accepts native Codex, normalized provider, and community worker
// quota shapes. Unreported numeric values remain nil, including wallet totals.
func Normalize(data []byte, fetchedAt time.Time) Snapshot {
	s := Snapshot{Version: Version, Status: Unknown, FetchedAt: fetchedAt.UTC(), CheckedAt: fetchedAt.UTC()}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil {
		return s
	}
	// Community results can already carry a richer normalized projection.
	if encoded, err := json.Marshal(root["allowance"]); err == nil {
		var existing Snapshot
		if json.Unmarshal(encoded, &existing) == nil && existing.Version == Version {
			return existing
		}
	}
	s.Plan = text(first(root, "plan", "plan_type"))
	s.Source = text(root["source"])
	s.ExpiresAt = timestamp(first(root, "expires_at", "until"))
	appendWindow := func(value any, name string) {
		if m, ok := value.(map[string]any); ok {
			s.Windows = append(s.Windows, normalizeWindow(m, name, fetchedAt))
		}
	}
	for _, key := range []string{"primary_window", "secondary_window"} {
		appendWindow(root[key], key)
	}
	if rate, ok := root["rate_limit"].(map[string]any); ok {
		for _, key := range []string{"primary_window", "secondary_window"} {
			appendWindow(rate[key], key)
		}
		s.ExplicitExhausted = rate["limit_reached"] == true || rate["allowed"] == false
	}
	if windows, ok := root["windows"].([]any); ok {
		for _, w := range windows {
			appendWindow(w, "")
		}
	}
	if limits, ok := root["additional_rate_limits"].([]any); ok {
		for _, raw := range limits {
			limit, _ := raw.(map[string]any)
			rate, _ := limit["rate_limit"].(map[string]any)
			for _, key := range []string{"primary_window", "secondary_window"} {
				m, ok := rate[key].(map[string]any)
				if !ok {
					continue
				}
				w := normalizeWindow(m, text(limit["limit_name"]), fetchedAt)
				w.Models = stringsOf(limit["models"])
				w.ExcludedModels = stringsOf(limit["excluded_models"])
				if len(w.Models) == 0 && len(w.ExcludedModels) == 0 {
					// A display label alone cannot define which models a limit stops.
					w.Informational = true
				}
				s.Windows = append(s.Windows, w)
			}
		}
	}
	if len(s.Windows) == 0 {
		w := normalizeWindow(root, "account", fetchedAt)
		if w.UsedPercent != nil || w.Balance != nil || w.Unlimited {
			s.Windows = append(s.Windows, w)
		}
	}
	s.ExplicitExhausted = s.ExplicitExhausted || root["limit_reached"] == true || root["allowed"] == false
	if credits, ok := root["credits"].(map[string]any); ok {
		s.Credits = &Credits{Balance: number(credits["balance"]), Unlimited: credits["unlimited"] == true, Unit: text(credits["unit"])}
		if b, ok := credits["has_credits"].(bool); ok {
			s.Credits.Available = &b
		}
	}
	// Largest allowance windows come first, as in Magpie's allowance model.
	sort.SliceStable(s.Windows, func(i, j int) bool { return s.Windows[i].PeriodSeconds > s.Windows[j].PeriodSeconds })
	if resets, ok := root["rate_limit_reset_credits"].(map[string]any); ok {
		if count := number(resets["available_count"]); count != nil {
			s.Resets = &ResetCredits{Count: int(*count), Unknown: *count > 0, CheckedAt: fetchedAt}
		}
	}
	s.Status = s.For("", fetchedAt, 0).Status
	return s
}

func normalizeWindow(m map[string]any, name string, now time.Time) Window {
	w := Window{Unlimited: m["unlimited"] == true, Period: text(m["period"]), Pool: text(m["pool"]), Unknown: m["unknown"] == true, Name: name, UsedPercent: number(first(m, "used_percent", "used")), Amount: number(m["amount"]), Limit: number(m["limit"]), Balance: number(m["balance"]), Unit: text(m["unit"]), Models: stringsOf(m["models"]), ExcludedModels: stringsOf(first(m, "excluded_models", "notModels")), Informational: m["aside"] == true || m["informational"] == true || m["text"] == true, Credits: m["credits"] == true}
	if len(w.Models) == 0 {
		if model := text(first(m, "model", "model_id")); model != "" {
			w.Models = []string{model}
		}
	}
	if value := text(first(m, "name", "label")); value != "" {
		w.Name = value
	}
	if value := number(first(m, "period_seconds", "limit_window_seconds", "span")); value != nil && *value <= 366*24*60*60 {
		w.PeriodSeconds = int64(*value)
	}
	w.ResetAt = timestamp(first(m, "reset_at", "resetsAt"))
	if w.ResetAt == nil {
		if seconds := number(m["reset_after_seconds"]); seconds != nil && *seconds <= 366*24*60*60 {
			t := now.Add(time.Duration(*seconds) * time.Second).UTC()
			w.ResetAt = &t
		}
	}
	w.ExpiresAt = timestamp(first(m, "expires_at", "runsOutAt"))
	if w.UsedPercent == nil && w.Amount != nil && w.Limit != nil && *w.Limit > 0 {
		used := *w.Amount / *w.Limit * 100
		w.UsedPercent = &used
	}
	if w.UsedPercent == nil {
		if remaining := number(m["remaining_percent"]); remaining != nil && *remaining <= 100 {
			used := 100 - *remaining
			w.UsedPercent = &used
		}
	}
	if w.UsedPercent != nil && *w.UsedPercent > 100 {
		used := 100.0
		w.UsedPercent = &used
	}
	return w
}

// Read handles both current projections and quota cached before this feature.
func Read(data []byte, fetchedAt time.Time) Snapshot {
	var root struct {
		Allowance Snapshot `json:"allowance"`
	}
	if json.Unmarshal(data, &root) == nil && root.Allowance.Version == Version {
		return root.Allowance
	}
	return Normalize(data, fetchedAt)
}

func first(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return nil
}

func text(v any) string { s, _ := v.(string); return s }

func stringsOf(v any) []string {
	var out []string
	if values, ok := v.([]any); ok {
		for _, value := range values {
			if s := text(value); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func number(v any) *float64 {
	var n float64
	var err error
	switch value := v.(type) {
	case json.Number:
		n, err = value.Float64()
	case float64:
		n = value
	case string:
		n, err = strconv.ParseFloat(value, 64)
	default:
		return nil
	}
	if err != nil || !finite(n) || n < 0 {
		return nil
	}
	return &n
}

func timestamp(v any) *time.Time {
	if s := text(v); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	if n := number(v); n != nil && *n > 0 && *n <= 253402300799 {
		t := time.Unix(int64(*n), 0).UTC()
		return &t
	}
	return nil
}
