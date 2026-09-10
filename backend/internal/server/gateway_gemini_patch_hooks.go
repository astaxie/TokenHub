package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func applyGeminiGatewayRequestPatch(payload *map[string]any, data json.RawMessage, model string, stream bool) error {
	if payload == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	var patched map[string]any
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if patchedModel, ok := patched["model"].(string); ok && strings.TrimSpace(patchedModel) != "" && strings.TrimSpace(patchedModel) != model {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if patchedStream, ok := patched["stream"].(bool); ok && patchedStream != stream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	if _, ok := patched["contents"].([]any); !ok {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	*payload = patched
	return nil
}
