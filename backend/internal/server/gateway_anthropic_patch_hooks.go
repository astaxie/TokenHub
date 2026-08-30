package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func applyAnthropicGatewayRequestPatch(req *anthropicMessagesRequest, data json.RawMessage) error {
	if req == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	originalModel := req.Model
	originalStream := req.Stream
	var patched map[string]any
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	model, _ := patched["model"].(string)
	if strings.TrimSpace(model) != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	stream, _ := patched["stream"].(bool)
	if stream != originalStream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	messages, ok := patched["messages"].([]any)
	if !ok || len(messages) == 0 {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	req.Raw = patched
	req.Messages = messages
	req.MaxTokens = int(int64FromAny(patched["max_tokens"]))
	return nil
}
