package server

import (
	"math"

	"tokenhub/backend/internal/metering"
)

// Usage is also sent to trace exporters without payload capture. Normalize its
// audio fields before persistence or export, including failed plugin responses.
func sanitizeAudioUsage(usage Usage) Usage {
	if usage.AudioDurationRaw != "" {
		if _, err := metering.Duration(usage.AudioDurationRaw); err != nil {
			usage.AudioDurationRaw = ""
		}
	}
	switch usage.AudioDurationSource {
	case "", "upstream", "plugin", "unreported", "invalid":
	default:
		usage.AudioDurationSource = "invalid"
	}
	switch usage.AudioDurationUnit {
	case "", "audio_second", "token":
	default:
		usage.AudioDurationUnit, usage.AudioDurationSource = "invalid", "invalid"
	}
	if seconds := usage.AudioDurationSeconds; seconds != nil && (*seconds < 0 || math.IsNaN(*seconds) || math.IsInf(*seconds, 0)) {
		usage.AudioDurationSeconds, usage.AudioDurationSource = nil, "invalid"
	}
	return usage
}

func sanitizeCompletionAudioUsage(completion GatewayCallCompletion) GatewayCallCompletion {
	completion.Usage = sanitizeAudioUsage(completion.Usage)
	completion.Attempts = sanitizeAudioAttemptUsage(completion.Attempts)
	completion.Call.RouteAttempts = sanitizeAudioAttemptUsage(completion.Call.RouteAttempts)
	return completion
}

func sanitizeAudioAttemptUsage(attempts []RouteAttempt) []RouteAttempt {
	if len(attempts) == 0 {
		return attempts
	}
	sanitized := append([]RouteAttempt(nil), attempts...)
	for index := range sanitized {
		sanitized[index].Usage = sanitizeAudioUsage(sanitized[index].Usage)
	}
	return sanitized
}
