package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type typeSafeNotifyingBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (b *typeSafeNotifyingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.ReadCloser.Read(p)
}

func TestTypeSafeCancellationDuringBodyRead(t *testing.T) {
	for _, operation := range []string{"systemone", "discovery"} {
		t.Run(operation, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer upstream.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			transport := upstream.Client().Transport
			adapter := TypeSafeAdapter{Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if err == nil {
					response.Body = &typeSafeNotifyingBody{ReadCloser: response.Body, started: started}
				}
				return response, err
			})}}
			request := systemOneTestRequest(t)
			done := make(chan error, 1)
			go func() {
				if operation == "discovery" {
					_, err := adapter.DiscoverModels(ctx, ProviderCreateRequest{BaseURL: upstream.URL, APIKey: "synthetic-key"})
					done <- err
					return
				}
				_, _, err := adapter.SystemOne(ctx, Provider{BaseURL: upstream.URL, APIKey: "synthetic-key"}, "jev-latest", request)
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("request ended before reading the body: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("response body read did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || shouldFailoverRoutedError(err, false) {
					t.Fatalf("cancellation was lost or allowed failover: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("body read did not stop after cancellation")
			}
		})
	}
}

func TestTypeSafeInterruptedBodyRemainsSanitizedAndRetryable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		writeFixture(t, w, `{"secret":"synthetic-key"`)
	}))
	defer upstream.Close()
	_, usage, err := (TypeSafeAdapter{Client: upstream.Client()}).SystemOne(context.Background(), Provider{BaseURL: upstream.URL, APIKey: "synthetic-key"}, "jev-latest", systemOneTestRequest(t))
	if err == nil || AsHTTPError(err).Code != "provider_invalid_response" || !usage.MeteringInvalid || !shouldFailoverRoutedError(err, false) {
		t.Fatalf("interrupted response must remain invalid and retryable: usage=%+v error=%v", usage, err)
	}
}
