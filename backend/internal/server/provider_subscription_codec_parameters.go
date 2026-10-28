package server

import "slices"

// Declare only controls represented by the selected Chat codec. A parameter
// profile would also admit fields that the Responses/Anthropic/Gemini bridges
// cannot encode. Individual codecs still validate value-specific constraints.
func subscriptionCodecParameters(protocol string, tools, reasoning bool) []string {
	var parameters []string
	switch protocol {
	case "chat", "responses", "anthropic", "gemini", "auto":
		parameters = []string{"max_tokens", "max_completion_tokens", "temperature", "top_p"}
	default:
		return nil
	}
	switch protocol {
	case "chat":
		parameters = append(parameters, "stop", "presence_penalty", "frequency_penalty", "response_format", "service_tier")
	case "responses":
		parameters = append(parameters, "response_format", "service_tier")
	case "anthropic", "gemini":
		parameters = append(parameters, "stop")
	}
	if tools {
		parameters = append(parameters, "tools", "tool_choice", "parallel_tool_calls")
	}
	if reasoning {
		parameters = append(parameters, "reasoning_effort")
	}
	return parameters
}

func copilotSubscriptionParameters(endpoints []string) []string {
	protocol := "chat"
	switch copilotAutoEndpoint(endpoints) {
	case "/responses":
		protocol = "responses"
	case "/v1/messages":
		protocol = "anthropic"
	}
	parameters := subscriptionCodecParameters(protocol, true, true)
	if slices.Contains(endpoints, "/responses") {
		// Copilot also exposes the native Responses endpoint when advertised by
		// this account. These are the native forms of the bridged Chat controls.
		parameters = append(parameters, "max_output_tokens", "text", "reasoning", "include")
	}
	return parameters
}
