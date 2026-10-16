package server

import (
	"encoding/json"
	"math"
	"strconv"

	"tokenhub/backend/internal/metering"
)

// AudioUsageEvidence is stored with metering settlements independently of
// request payload retention. Nil seconds means unreported, not measured zero.
type AudioUsageEvidence struct {
	Seconds         *float64 `json:"seconds,omitempty"`
	Source          string   `json:"source"`
	BillingUnit     string   `json:"billing_unit"`
	ReportedSeconds string   `json:"reported_seconds,omitempty"`
}

func captureAudioDuration(usage Usage, body map[string]any, allowVerbose ...bool) Usage {
	reported, _ := body["usage"].(map[string]any)
	value, present := body["duration"]
	present = present && len(allowVerbose) > 0 && allowVerbose[0]
	usage.AudioDurationUnit = "token"
	if present && !audioResponseHasTokenUsage(reported) {
		usage.AudioDurationUnit = "audio_second"
	}
	if reported["type"] == "duration" {
		value, present = reported["seconds"]
		usage.AudioDurationSource = "unreported"
		usage.AudioDurationUnit = "audio_second"
	}
	if !present {
		if usage.AudioDurationSource == "" {
			usage.AudioDurationUnit = ""
		}
		return usage
	}
	encoded, err := json.Marshal(value)
	var seconds float64
	if value == nil || err != nil || json.Unmarshal(encoded, &seconds) != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		usage.AudioDurationSource = "invalid"
		usage.MeteringInvalid = true
		return usage
	}
	if _, err := metering.Duration(string(encoded)); err != nil {
		usage.AudioDurationSource = "invalid"
		usage.MeteringInvalid = true
		return usage
	}
	usage.AudioDurationSeconds = &seconds
	usage.AudioDurationSource = "upstream"
	usage.AudioDurationRaw = string(encoded)
	return usage
}

func audioDurationEvidence(usage Usage) *AudioUsageEvidence {
	if usage.AudioDurationSeconds == nil && usage.AudioDurationSource == "" {
		return nil
	}
	evidence := &AudioUsageEvidence{Seconds: usage.AudioDurationSeconds, Source: usage.AudioDurationSource, ReportedSeconds: usage.AudioDurationRaw, BillingUnit: usage.AudioDurationUnit}
	if evidence.ReportedSeconds != "" {
		if _, err := metering.Duration(evidence.ReportedSeconds); err != nil {
			evidence.ReportedSeconds = ""
		}
	}
	if evidence.BillingUnit == "" {
		evidence.BillingUnit = "audio_second"
		if usage.PromptTokens != 0 || usage.CompletionTokens != 0 || usage.TotalTokens != 0 {
			evidence.BillingUnit = "token"
		}
	}
	if evidence.Source == "" {
		evidence.Source = "plugin"
	}
	if evidence.Seconds != nil && (*evidence.Seconds < 0 || math.IsNaN(*evidence.Seconds) || math.IsInf(*evidence.Seconds, 0)) {
		evidence.Seconds = nil
		evidence.Source = "invalid"
	}
	return evidence
}

func validateAudioDuration(usage Usage) error {
	e := audioDurationEvidence(usage)
	if e == nil {
		return nil
	}
	if (e.BillingUnit != "audio_second" && e.BillingUnit != "token") || e.Source == "invalid" || (e.Source != "upstream" && e.Source != "plugin" && e.Source != "unreported") ||
		(e.Source == "unreported") != (e.Seconds == nil) {
		return NewHTTPError(502, "invalid_provider_usage", "Provider returned invalid audio duration usage")
	}
	return nil
}

func audioEvidenceQuantity(e *AudioUsageEvidence) string {
	// The original numeric representation avoids precision loss for providers
	// that report decimal seconds. Plugin patches must agree with that value.
	if raw, err := strconv.ParseFloat(e.ReportedSeconds, 64); err == nil && raw == *e.Seconds {
		return e.ReportedSeconds
	}
	return strconv.FormatFloat(*e.Seconds, 'f', -1, 64)
}

func audioResponseHasTokenUsage(reported map[string]any) bool {
	if reported["type"] == "tokens" {
		return true
	}
	for _, name := range []string{"prompt_tokens", "input_tokens", "completion_tokens", "output_tokens", "total_tokens"} {
		if _, present := reported[name]; present {
			return true
		}
	}
	return false
}

func audioTranscriptionEndpoint(endpoints []string) bool {
	if len(endpoints) == 0 {
		return false
	}
	switch endpoints[0] {
	case "/audio/transcriptions", "/audio/translations", "audio/transcriptions", "audio/translations":
		return true
	}
	return false
}

func audioDurationBillable(usage Usage) bool {
	evidence := audioDurationEvidence(usage)
	return evidence != nil && evidence.BillingUnit == "audio_second"
}

// Streaming providers can report tokens and duration in separate events. Keep
// their latest independent observations; a duration-only event is not zero tokens.
type transcriptionStreamUsage struct {
	usage                  Usage
	tokensReported         bool
	nativeDurationReported bool
	nativeDurationMeasured bool
}

func (s *transcriptionStreamUsage) capture(probe providerStreamEventProbe) bool {
	next, ok := mediaUsageFromProbe(probe, true)
	if !ok {
		return false
	}
	var reported map[string]any
	_ = decodeResponsesJSON(probe["usage"], &reported)
	native := reported["type"] == "duration"
	previous := s.usage
	if audioResponseHasTokenUsage(reported) {
		s.usage = next
		s.usage.AudioDurationSeconds, s.usage.AudioDurationSource = previous.AudioDurationSeconds, previous.AudioDurationSource
		s.usage.AudioDurationRaw, s.usage.AudioDurationUnit = previous.AudioDurationRaw, previous.AudioDurationUnit
		s.tokensReported = true
	}
	if next.AudioDurationSource != "" && (!s.nativeDurationMeasured || native) &&
		(next.AudioDurationSeconds != nil || previous.AudioDurationSeconds == nil || next.AudioDurationSource == "invalid") {
		s.usage.AudioDurationSeconds, s.usage.AudioDurationSource = next.AudioDurationSeconds, next.AudioDurationSource
		s.usage.AudioDurationRaw = next.AudioDurationRaw
	}
	s.nativeDurationReported = s.nativeDurationReported || native
	s.nativeDurationMeasured = s.nativeDurationMeasured || native && next.AudioDurationSeconds != nil
	if s.usage.AudioDurationSource != "" {
		s.usage.AudioDurationUnit = "audio_second"
		if s.tokensReported && !s.nativeDurationReported {
			s.usage.AudioDurationUnit = "token"
		}
	}
	s.usage.MeteringInvalid = previous.MeteringInvalid || next.MeteringInvalid
	return true
}
