package server

// Only canonical proxy failures cross the worker boundary. Neither transport
// error strings nor upstream response fields can supply public diagnostics.
func subscriptionWorkerEgressError(code string) error {
	stage := ""
	switch code {
	case "provider_proxy_config_error":
		stage = "config"
	case "provider_proxy_connect_failed":
		stage = "connect"
	case "provider_proxy_auth_failed":
		stage = "auth"
	case "provider_proxy_timeout":
		stage = "timeout"
	default:
		return nil
	}
	return newProviderProxyTransportError(stage, nil)
}

func subscriptionWorkerNetworkFailure(requestID string, err error) map[string]any {
	message := map[string]any{"operation": "network.error", "request_id": requestID, "error": "upstream_request_failed"}
	if guarded := providerEgressFailure(err); guarded != nil {
		if failure := subscriptionWorkerEgressError(AsHTTPError(guarded).Code); failure != nil {
			public := AsHTTPError(failure)
			message["error"], message["status"] = public.Code, public.Status
		}
	}
	return message
}
