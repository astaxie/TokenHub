package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type subscriptionWorkerTransport struct {
	adapter    *SubscriptionWorkerAdapter
	process    *subscriptionWorkerProcess
	resourceID string
	model      subscriptionWorkerModel
	stream     bool
}

type subscriptionWorkerBody struct {
	*io.PipeReader
	cancel context.CancelFunc
	done   <-chan struct{}
}

func (b *subscriptionWorkerBody) Close() error {
	select {
	case <-b.done:
	default:
		b.cancel()
	}
	return b.PipeReader.Close()
}

func (t *subscriptionWorkerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, NewHTTPError(400, "subscription_body_required", "Subscription inference requires a JSON body")
	}
	defer req.Body.Close()
	data, err := io.ReadAll(io.LimitReader(req.Body, subscriptionWorkerMessageLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > subscriptionWorkerMessageLimit || !json.Valid(data) {
		return nil, NewHTTPError(400, "subscription_body_invalid", "Subscription inference requires a bounded JSON body")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil || payload == nil {
		return nil, NewHTTPError(400, "subscription_body_invalid", "Subscription inference requires a JSON object")
	}
	sessionID := subscriptionWorkerSessionID(req.Context(), payload)
	if _, exists := payload["prompt_cache_key"]; exists {
		payload["prompt_cache_key"], _ = json.Marshal(sessionID)
	}
	data, err = json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	command := t.adapter.command(t.resourceID, "fetch")
	command["session_id"] = sessionID
	command["model"], command["protocol"], command["body"], command["stream"] = t.model.ID, t.model.Protocol, json.RawMessage(data), t.stream
	headers := http.Header{}
	for _, name := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"} {
		if values := req.Header.Values(name); len(values) > 0 {
			headers[name] = values
		}
	}
	command["headers"] = headers
	secrets := &subscriptionWorkerSecrets{}
	ctx, cancel := context.WithCancel(context.WithValue(req.Context(), subscriptionWorkerSecretsKey{}, secrets))
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = writer.CloseWithError(ctx.Err())
		case <-done:
		}
	}()
	type responseResult struct {
		response *http.Response
		err      error
	}
	ready := make(chan responseResult, 1)
	go func() {
		defer close(done)
		defer cancel()
		received := false
		failedStatus := false
		_, invokeErr := t.process.invoke(ctx, command, func(event subscriptionWorkerEvent) error {
			switch event.Event {
			case "response":
				if received || event.Status < 100 || event.Status > 599 {
					return errors.New("subscription runtime returned an invalid response")
				}
				received = true
				failedStatus = event.Status < 200 || event.Status >= 300
				responseHeaders := event.Headers.Clone()
				if failedStatus {
					responseHeaders = http.Header{"Content-Type": []string{"application/json"}}
				}
				responseHeaders.Del("Content-Length")
				responseHeaders.Del("Content-Encoding")
				var body io.ReadCloser = &subscriptionWorkerBody{PipeReader: reader, cancel: cancel, done: done}
				if t.stream && !failedStatus {
					body = newSubscriptionErrorStream(body, secrets.provider, 0)
				} else if !failedStatus {
					body = &subscriptionWorkerJSONBody{body: body, secrets: secrets}
				}
				ready <- responseResult{response: &http.Response{StatusCode: event.Status, Status: strconv.Itoa(event.Status) + " " + http.StatusText(event.Status), Header: responseHeaders, Body: body, Request: req}}
			case "chunk":
				if !received {
					return errors.New("subscription runtime returned data before headers")
				}
				if failedStatus {
					return nil
				}
				chunk, decodeErr := base64.StdEncoding.DecodeString(event.Data)
				if decodeErr != nil || len(chunk) > 1<<20 {
					return errors.New("subscription runtime returned an invalid data chunk")
				}
				_, writeErr := writer.Write(chunk)
				return writeErr
			}
			return nil
		})
		if !received {
			if invokeErr == nil {
				invokeErr = NewHTTPError(502, "subscription_response_missing", "Subscription runtime returned no response")
			}
			ready <- responseResult{err: invokeErr}
			_ = writer.CloseWithError(invokeErr)
			return
		}
		if failedStatus && invokeErr == nil {
			_, invokeErr = io.Copy(writer, strings.NewReader(`{"error":{"message":"Subscription upstream request failed","type":"subscription_upstream_error"}}`))
		}
		_ = writer.CloseWithError(invokeErr)
	}()
	select {
	case result := <-ready:
		if result.err != nil {
			cancel()
			_ = reader.Close()
		}
		return result.response, result.err
	case <-ctx.Done():
		cancel()
		_ = reader.Close()
		return nil, ctx.Err()
	}
}

func subscriptionWorkerSessionID(ctx context.Context, payload map[string]json.RawMessage) string {
	caller, _ := ctx.Value(subscriptionCallerKey{}).(string)
	if caller == "" {
		return NewID("subscription_session")
	}
	grouping := string(payload["prompt_cache_key"])
	if grouping == "" {
		grouping = string(payload["user"])
	}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(payload["messages"], &messages)
	first := ""
	for _, message := range messages {
		if string(message["role"]) == `"user"` {
			encoded, _ := json.Marshal(message)
			first = string(encoded)
			break
		}
	}
	sum := sha256.Sum256([]byte(caller + "\x00" + grouping + "\x00" + first))
	return "tokenhub-" + hex.EncodeToString(sum[:])
}
