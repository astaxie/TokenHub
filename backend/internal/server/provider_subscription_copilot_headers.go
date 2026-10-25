package server

import "encoding/json"

func copilotSubscriptionHasImage(data []byte) bool {
	var body struct {
		Messages any `json:"messages"`
		Input    any `json:"input"`
	}
	if json.Unmarshal(data, &body) != nil {
		return false
	}
	return copilotSubscriptionImageContent(body.Messages) || copilotSubscriptionImageContent(body.Input)
}

// Inspect protocol content blocks, never user text, tool arguments, or schemas.
func copilotSubscriptionImageContent(value any) bool {
	switch content := value.(type) {
	case []any:
		for _, item := range content {
			if copilotSubscriptionImageContent(item) {
				return true
			}
		}
	case map[string]any:
		switch content["type"] {
		case "image", "image_url", "input_image":
			return true
		case "function_call_output":
			return copilotSubscriptionImageContent(content["output"])
		}
		return copilotSubscriptionImageContent(content["content"])
	}
	return false
}
