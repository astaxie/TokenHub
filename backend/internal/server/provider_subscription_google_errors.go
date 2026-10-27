package server

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func checkGoogleSubscriptionResponse(response *http.Response) error {
	if response.StatusCode < http.StatusBadRequest {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		// Only a 403 body can refine the failure classification. Decode one
		// bounded object without waiting for EOF, and cap the entire inspection
		// so a stalled or trickling diagnostic cannot delay route failover.
		timer := time.AfterFunc(time.Second, func() { _ = response.Body.Close() })
		defer timer.Stop()
		var body struct {
			Error map[string]any `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, providerErrorBodyPrefix)).Decode(&body) == nil {
			if err := googleSubscriptionLocationFailure(response.StatusCode, body.Error); err != nil {
				return err
			}
		}
	}
	return newProviderHTTPError(response.StatusCode, response.Header, nil)
}

func googleSubscriptionLocationFailure(status int, detail map[string]any) error {
	if status != http.StatusForbidden {
		return nil
	}
	details, _ := detail["details"].([]any)
	for _, item := range details {
		info, _ := item.(map[string]any)
		if info["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || info["domain"] != "cloudaicompanion.googleapis.com" || info["reason"] != "UNSUPPORTED_LOCATION" {
			continue
		}
		// Regional eligibility does not invalidate OAuth credentials. Use a fixed
		// diagnostic so upstream messages and account metadata stay private.
		failure := NewHTTPError(http.StatusBadGateway, "google_subscription_location_unsupported", "Google subscription access is unavailable in the location associated with this request. Check the account's regional eligibility and the TokenHub server's outbound location.")
		failure.UpstreamStatus = status
		return &ProviderInvocationError{Err: failure, Disposition: ProviderErrorResourceBroken}
	}
	return nil
}
