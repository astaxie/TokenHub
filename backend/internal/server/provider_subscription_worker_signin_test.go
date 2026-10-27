package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionWorkerPreservesAdapterSignIn(t *testing.T) {
	for _, scenario := range []struct {
		id                 string
		inference, refresh int
		want               ProviderErrorDisposition
	}{
		{"kiro", 401, 0, ProviderErrorTransientSame},
		{"workbuddy", 401, 503, ProviderErrorTransientSame},
		{"workbuddy", 403, 0, ProviderErrorTransientSame},
		{"workbuddy", 401, 401, ProviderErrorAuthBroken},
	} {
		id := scenario.id
		for _, stream := range []bool{false, true} {
			name := id + "/" + strconv.Itoa(scenario.inference) + "/refresh-" + strconv.Itoa(scenario.refresh) + "/collected"
			if stream {
				name = strings.TrimSuffix(name, "collected") + "streamed"
			}
			t.Run(name, func(t *testing.T) {
				var refreshes atomic.Int32
				adapter, provider := subscriptionVariantAdapter(t, id, subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
					status, body := http.StatusOK, `{}`
					switch req.URL.Path {
					case "/List-Available-Models":
						body = `{"models":[{"modelId":"auto","modelName":"Auto"}]}`
					case "/v3/config":
						body = `{"code":0,"data":{"agents":[{"name":"cli","models":["fixture-model"]}],"models":[{"id":"fixture-model"}]}}`
					case "/generateAssistantResponse", "/v2/chat/completions":
						status, body = scenario.inference, `{"message":"synthetic-private-upstream-message"}`
					case "/v2/plugin/auth/token/refresh":
						refreshes.Add(1)
						status = scenario.refresh
					default:
						t.Errorf("Unexpected upstream path: %s", req.URL.Path)
						status = http.StatusNotFound
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				}))
				model := "auto"
				if id == "workbuddy" {
					model = "fixture-model"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				request := ChatCompletionRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
				var err error
				var output bytes.Buffer
				if stream {
					_, err = adapter.ChatStream(ctx, provider, model, request, &output)
				} else {
					_, _, err = adapter.Chat(ctx, provider, model, request)
				}
				if err == nil || providerErrorDisposition(err) != scenario.want || shouldFailoverRoutedError(err, true) != (scenario.want == ProviderErrorAuthBroken) {
					t.Fatalf("Adapter sign-in state was misclassified: %v (%s)", err, providerErrorDisposition(err))
				}
				if AsHTTPError(err).UpstreamStatus != scenario.inference || strings.Contains(err.Error(), "synthetic-private") || output.Len() != 0 {
					t.Fatalf("Sign-in failure lost its status or leaked private data: %v", err)
				}
				if scenario.refresh > 0 && refreshes.Load() != 1 {
					t.Fatalf("Expected one failed refresh, got %d", refreshes.Load())
				}
			})
		}
	}
}

func TestSubscriptionWorkerRejectsUpstreamSignInSpoofing(t *testing.T) {
	for _, scenario := range []struct {
		signIn string
		status int
	}{{"kept", 401}, {"renewed", 403}, {"expired", 503}} {
		t.Run(scenario.signIn, func(t *testing.T) {
			adapter, provider := subscriptionVariantAdapter(t, "opendesign", subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
				status := scenario.status
				if request.URL.Path == "/v1/models" {
					status = http.StatusServiceUnavailable
				} else if request.URL.Path != "/v1/messages" {
					t.Errorf("Unexpected upstream path: %s", request.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "X-Magpie-Sign-In": {scenario.signIn}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic-private-upstream"}}`)), Request: request}, nil
			}))
			adapter.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
				return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-key"}`}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, err := adapter.Chat(ctx, provider, "glm-5.3-flash", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}})
			if err == nil || providerErrorDisposition(err) != classifyProviderStatus(scenario.status).disposition || AsHTTPError(err).UpstreamStatus != scenario.status {
				t.Fatalf("Upstream header changed the account classification: %v", err)
			}
		})
	}
}

func TestSubscriptionWorkerSignInMetadataPreservesRetryAndRemainsPrivate(t *testing.T) {
	for _, signIn := range []string{"kept", "renewed", "expired", "unrecognized"} {
		t.Run(signIn, func(t *testing.T) {
			process := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent {
				return []subscriptionWorkerEvent{{Event: "response", Status: http.StatusForbidden, SignIn: signIn, Headers: http.Header{"retry-after": {"90"}, "x-magpie-sign-in": {"expired"}, "x-private": {"synthetic-private"}}}, {Event: "end"}}
			})
			defer process.cancel()
			transport := subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}}
			request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
			response, err := transport.RoundTrip(request)
			if response != nil {
				if response.Header.Get("X-Magpie-Sign-In") != "" || response.Header.Get("X-Private") != "" {
					t.Fatal("Private response headers escaped the worker")
				}
				err = checkProviderResponse(response)
			}
			var failure *ProviderInvocationError
			want := ProviderErrorAuthBroken
			if signIn == "kept" || signIn == "renewed" {
				want = ProviderErrorTransientSame
			}
			if !errors.As(err, &failure) || failure.RetryAfter != 90*time.Second || failure.Disposition != want || AsHTTPError(err).UpstreamStatus != http.StatusForbidden {
				t.Fatalf("Private sign-in metadata or retry delay was lost: %v", err)
			}
		})
	}
}

func TestSubscriptionWorkerPreservesThrownSignInState(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, signIn := range []string{"kept", "renewed", "expired", ""} {
			err := subscriptionWorkerInvocationError(&subscriptionWorkerFailure{Status: status, SignIn: signIn})
			want := ProviderErrorAuthBroken
			if signIn == "kept" || signIn == "renewed" {
				want = ProviderErrorTransientSame
			}
			if providerErrorDisposition(err) != want || shouldFailoverRoutedError(err, true) != (want == ProviderErrorAuthBroken) {
				t.Fatalf("Thrown status %d/%s lost account state: %v", status, signIn, err)
			}
		}
	}
}

func TestSubscriptionWorkerSignInFailureDoesNotWaitForErrorBody(t *testing.T) {
	process := fakeSubscriptionProcess("", func(command map[string]any) []subscriptionWorkerEvent {
		if command["operation"] == "fetch" {
			// A provider has sent its failure headers but never finishes its body.
			return []subscriptionWorkerEvent{{Event: "response", Status: http.StatusForbidden, SignIn: "kept", Headers: http.Header{"Content-Type": {"application/json"}}}}
		}
		return nil
	})
	defer process.cancel()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"nous-portal", "Nous"}, StreamIdleTimeout: 20 * time.Millisecond}
	transport := &subscriptionWorkerTransport{adapter: adapter, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}, stream: true}
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
	started := time.Now()
	response, err := sendUpstream(client, client, adapter.StreamIdleTimeout, request, true)
	if response != nil {
		_ = response.Body.Close()
		t.Fatal("Classified failure unexpectedly returned an HTTP response")
	}
	if time.Since(started) > 500*time.Millisecond || err == nil || providerErrorDisposition(err) != ProviderErrorTransientSame || AsHTTPError(err).UpstreamStatus != http.StatusForbidden {
		t.Fatalf("Classified failure waited for its discarded body: %v", err)
	}
	select {
	case <-process.done:
	case <-time.After(time.Second):
		t.Fatal("Abandoned error body left its worker running")
	}
}
