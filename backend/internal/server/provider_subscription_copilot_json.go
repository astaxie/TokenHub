package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Inspect non-streaming responses while the exact exchanged bearer is available.
// Normal JSON is replayed unchanged to the existing protocol-specific decoder.
func checkCopilotSubscriptionJSON(ctx context.Context, response *http.Response, provider Provider, accountToken, sessionToken string) error {
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
	redaction := copilotSubscriptionRedactionProvider(provider, accountToken, sessionToken)
	if _, err := subscriptionJSONError(data, redaction); err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return nil
}
