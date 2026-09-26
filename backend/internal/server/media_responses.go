package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func decodeResponsesJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// Generations are side effects, and a task query must see the current status.
// Media models using the existing Responses/Chat surfaces bypass response caches.
func mediaRequestBypassesCache(call CallContext, payload any) bool {
	switch payload.(type) {
	case ResponsesRequest, ChatCompletionRequest:
	default:
		return false
	}
	return modelHasMediaOutput(call.Model)
}

func modelHasMediaOutput(model Model) bool {
	modalities := append([]string{model.Modality}, model.OutputModalities...)
	for _, modality := range modalities {
		switch strings.ToLower(modality) {
		case "image", "audio", "video", "music":
			return true
		}
	}
	return false
}

// Responses and Chat also submit billable media tasks. A lost response or an
// upstream timeout/5xx does not establish that the generation was rejected.
// Keep definite rejections and local admission failures eligible for failover.
func classifyMediaAttemptFailure(call CallContext, usage Usage, err error) (Usage, error) {
	if err == nil || !modelHasMediaOutput(call.Model) || (call.RouteProtocol != providerRouteProtocolResponses && call.RouteProtocol != providerRouteProtocolChatCompletions) {
		return usage, err
	}
	switch providerErrorDisposition(err) {
	case "", ProviderErrorTransientSame:
	default:
		return usage, err
	}
	if errors.Is(err, ErrCoordinationLeaseLost) || errors.Is(err, context.Canceled) {
		return usage, err
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.UpstreamStatus < http.StatusInternalServerError && httpErr.UpstreamStatus != http.StatusRequestTimeout {
		return usage, err
	}
	usage.MeteringInvalid = true
	return usage, uncertainMediaSubmission(err)
}
