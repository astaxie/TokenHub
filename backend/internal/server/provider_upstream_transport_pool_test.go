package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderUpstreamTransportRotationUsesNewPoolWithoutInterruptingInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			close(started)
			<-release
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	}))
	defer upstream.Close()

	var dials atomic.Int32
	factory := func(providerSyntheticDNSSnapshot) *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		dialer := &net.Dialer{Timeout: time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			return dialer.DialContext(ctx, network, address)
		}
		return transport
	}
	store := NewMemoryStore()
	store.CreateResource("settings", syntheticDNSSettings(false, defaultSyntheticDNSCIDRs))
	policy := newProviderSyntheticDNSPolicy(store)
	pool := newProviderUpstreamTransportPool(policy, factory)
	client := &http.Client{Transport: pool}

	firstDone := make(chan error, 1)
	go func() {
		response, err := client.Get(upstream.URL + "/slow")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			err = response.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the in-flight request")
	}

	enabled := syntheticDNSSettings(true, defaultSyntheticDNSCIDRs)
	policy.applySetting(&enabled)
	response, err := client.Get(upstream.URL + "/fast")
	if err != nil {
		t.Fatalf("new generation request failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("rotation interrupted the in-flight request: %v", err)
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("expected each transport generation to dial once, got %d dials", got)
	}
}

func TestProviderUpstreamTransportRotationKeepsPreDialRequestPolicySnapshot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	}))
	defer upstream.Close()

	oldDialStarted := make(chan struct{})
	releaseOldDial := make(chan struct{})
	var generations atomic.Int32
	factory := func(snapshot providerSyntheticDNSSnapshot) *http.Transport {
		generation := generations.Add(1)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		dialer := &net.Dialer{Timeout: time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if generation == 1 {
				close(oldDialStarted)
				select {
				case <-releaseOldDial:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if !snapshot.allowsResolvedIPContext(ctx, net.ParseIP("198.18.0.1")) {
				return nil, errors.New("synthetic DNS policy snapshot rejected dial")
			}
			return dialer.DialContext(ctx, network, address)
		}
		return transport
	}
	store := NewMemoryStore()
	store.CreateResource("settings", syntheticDNSSettings(true, defaultSyntheticDNSCIDRs))
	policy := newProviderSyntheticDNSPolicy(store)
	client := &http.Client{Transport: newProviderUpstreamTransportPool(policy, factory)}

	firstDone := make(chan error, 1)
	go func() {
		response, err := client.Get(upstream.URL)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			err = response.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-oldDialStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the old request before dial completion")
	}

	disabled := syntheticDNSSettings(false, defaultSyntheticDNSCIDRs)
	policy.applySetting(&disabled)
	close(releaseOldDial)
	if err := <-firstDone; err != nil {
		t.Fatalf("expected the pre-dial request to retain its original policy snapshot: %v", err)
	}
	if _, err := client.Get(upstream.URL); err == nil || !strings.Contains(err.Error(), "policy snapshot rejected dial") {
		t.Fatalf("expected a new request to use the disabled policy generation, got %v", err)
	}
}
