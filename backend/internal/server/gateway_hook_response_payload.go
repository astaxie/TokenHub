package server

import "encoding/json"

func decodeGatewayHookResponsePayload(data json.RawMessage, target *any, protocol string) error {
	if protocol == providerRouteProtocolSystemOne {
		// Preserve duplicate members and exact numeric text until native validation.
		// Decoding through a generic map first would erase both distinctions.
		if len(data) > systemOneMaxJSONBytes {
			return invalidSystemOneResponse()
		}
		*target = append(json.RawMessage(nil), data...)
		return nil
	}
	return decodeGatewayHookPayload(data, target, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response")
}

// Response patches must be checked before a later hook can normalize away
// duplicate members. Provider-call decoding retains raw invalid answers so the
// native invocation path can account for independently valid usage on failure.
func decodeGatewayPostHookResponsePayload(data json.RawMessage, target *any, protocol string) error {
	if protocol == providerRouteProtocolSystemOne {
		if len(data) > systemOneMaxJSONBytes {
			return invalidSystemOneResponse()
		}
		var response SystemOneResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return invalidSystemOneResponse()
		}
	}
	return decodeGatewayHookResponsePayload(data, target, protocol)
}
