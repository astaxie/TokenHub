package server

import (
	"context"
	"io"
	"net/http"
)

// Inference always requests SSE. Inspect JSON failures before opening a public
// stream, while the bearer used for this request is available for redaction.
func rejectChatGPTSubscriptionJSON(ctx context.Context, response *http.Response, provider Provider) error {
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && AsHTTPError(err).Code == "subscription_stream_idle" {
		return err
	}
	if err != nil || len(data) > 4<<20 {
		return NewHTTPError(http.StatusBadGateway, "subscription_invalid_response", "Subscription upstream returned an invalid response")
	}
	if _, err := subscriptionJSONError(data, provider); err != nil {
		return err
	}
	return NewHTTPError(http.StatusBadGateway, "subscription_invalid_response", "ChatGPT API did not return a Responses stream")
}
