package server

import (
	"io"
	"net/http"
	"sort"
	"strings"
)

const subscriptionResponsesResponseLimit = 32 << 20

// Subscription Responses are always streamed upstream. Decode bounded SSE
// frames for both buffered requests and public passthrough, retaining terminal
// failure usage and rejecting incomplete response objects before hydration.
func consumeSubscriptionResponsesStream(body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	return consumeSubscriptionResponsesStreamWithLimit(body, destination, subscriptionResponsesResponseLimit)
}

func consumeSubscriptionResponsesStreamWithLimit(body io.Reader, destination io.Writer, limit int) (map[string]any, string, Usage, error) {
	decoder := newSSEDecoder(body)
	var response map[string]any
	var text strings.Builder
	var usage Usage
	outputs := make(map[int]map[string]any)
	var outputOrder []int
	nextUnindexedOutput := -1
	total := 0
	for {
		event, err := decoder.Next()
		if err != nil {
			if err == io.EOF {
				err = NewHTTPError(http.StatusBadGateway, "subscription_stream_incomplete", "Subscription stream ended before a terminal response event")
			}
			return response, text.String(), usage, err
		}
		if len(event.Data) > limit-total {
			return response, text.String(), usage, invalidProviderResponseError("Subscription Responses completion exceeds the response limit")
		}
		total += len(event.Data)
		if strings.TrimSpace(event.Data) == "[DONE]" {
			return response, text.String(), usage, NewHTTPError(http.StatusBadGateway, "subscription_stream_incomplete", "Subscription stream ended before a terminal response event")
		}
		payload, present, err := decodeSSEDataNumbers(event)
		if err != nil || present && payload == nil {
			return response, text.String(), usage, invalidProviderResponseError("Subscription provider returned an invalid Responses stream event")
		}
		if reported, ok := payload["usage"].(map[string]any); ok && len(reported) > 0 {
			usage = usageFromMap(payload)
		}
		if nested, ok := payload["response"].(map[string]any); ok {
			response = nested
			if reported, ok := nested["usage"].(map[string]any); ok && len(reported) > 0 {
				usage = usageFromMap(nested)
			}
		}
		eventType, _ := payload["type"].(string)
		if eventType == "" {
			eventType = event.Event
		}
		completed := false
		switch eventType {
		case "response.output_text.delta":
			delta, _ := payload["delta"].(string)
			text.WriteString(delta)
		case "response.output_item.done":
			if item, ok := payload["item"].(map[string]any); ok {
				index := nextUnindexedOutput
				if rawIndex, exists := payload["output_index"]; exists {
					index = int(int64FromAny(rawIndex))
				} else {
					nextUnindexedOutput--
				}
				if _, exists := outputs[index]; !exists {
					outputOrder = append(outputOrder, index)
				}
				outputs[index] = item
			}
		case "response.completed", "response.done", "response.incomplete":
			terminal, _ := payload["response"].(map[string]any)
			if terminal == nil {
				return response, text.String(), usage, invalidProviderResponseError("Subscription terminal event is missing response")
			}
			response = terminal
			if output, _ := anySlice(response["output"]); len(output) == 0 && len(outputOrder) > 0 {
				// Completed items may arrive out of order. Sort only indexed slots;
				// legacy unindexed items retain their original arrival positions.
				indexed := make([]int, 0, len(outputOrder))
				for _, index := range outputOrder {
					if index >= 0 {
						indexed = append(indexed, index)
					}
				}
				sort.Ints(indexed)
				output = make([]any, 0, len(outputOrder))
				for _, index := range outputOrder {
					if index >= 0 {
						index, indexed = indexed[0], indexed[1:]
					}
					output = append(output, outputs[index])
				}
				response["output"] = output
			}
			completed = true
		}
		if destination != nil {
			if _, err := destination.Write(event.Raw); err != nil {
				return response, text.String(), usage, err
			}
			if flusher, ok := destination.(streamFlusher); ok {
				flusher.Flush()
			}
		}
		if providerStreamEventIsError(event) {
			return response, text.String(), usage, codexStreamEventError(payload)
		}
		if completed {
			if text.Len() == 0 {
				text.WriteString(codexResponseOutputText(response))
			}
			return response, text.String(), usage, nil
		}
	}
}
