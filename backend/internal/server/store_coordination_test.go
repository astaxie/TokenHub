package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseHeartbeatRetriesTransientRenewalErrors(t *testing.T) {
	const ttl = 600 * time.Millisecond
	var attempts atomic.Int64
	renewed := make(chan struct{})
	heartbeat := startLeaseHeartbeat(context.Background(), ttl, ttl, func(context.Context) (time.Duration, bool, error) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			return 0, false, errors.New("transient database error")
		}
		if attempt == 2 {
			close(renewed)
		}
		return ttl, true, nil
	})

	select {
	case <-renewed:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not retry the transient renewal error")
	}
	select {
	case <-heartbeat.ctx.Done():
		t.Fatalf("heartbeat canceled after a recoverable error: %v", context.Cause(heartbeat.ctx))
	default:
	}
	if err := stopLeaseHeartbeat(heartbeat); err != nil {
		t.Fatalf("stop heartbeat: %v", err)
	}
}

func TestLeaseHeartbeatCancelsWhenOwnershipIsLost(t *testing.T) {
	const ttl = 600 * time.Millisecond
	heartbeat := startLeaseHeartbeat(context.Background(), ttl, ttl, func(context.Context) (time.Duration, bool, error) {
		return 0, false, nil
	})

	select {
	case <-heartbeat.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not cancel after ownership was lost")
	}
	if !errors.Is(context.Cause(heartbeat.ctx), ErrCoordinationLeaseLost) {
		t.Fatalf("unexpected cancellation cause: %v", context.Cause(heartbeat.ctx))
	}
	if err := stopLeaseHeartbeat(heartbeat); !errors.Is(err, ErrCoordinationLeaseLost) {
		t.Fatalf("expected lease loss from stop, got %v", err)
	}
}

func TestFinishProviderAttemptReleasesLeaseAfterAccountingFailure(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "provider-fallback-release",
		Name:    "Fallback release",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "resource-fallback-release",
		ProviderID:     provider.ID,
		Name:           "Fallback release",
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseID, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leaseID == "" {
		t.Fatal("expected provider concurrency lease")
	}
	if err := store.db.Delete(&ProviderResource{}, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}

	store.FinishProviderResourceAttempt(resource.ID, leaseID, true, Usage{})
	var count int64
	if err := store.db.Model(&InFlightLease{}).Where("id = ?", leaseID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("provider lease remained after accounting rollback: count=%d", count)
	}
}

func TestReleaseProviderCapacityDoesNotRecordProviderFailure(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "provider-neutral-release",
		Name:    "Neutral release",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "resource-neutral-release",
		ProviderID:     provider.ID,
		Name:           "Neutral release",
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Update("failure_count", 2).Error; err != nil {
		t.Fatal(err)
	}
	leaseID, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}

	store.ReleaseProviderResourceCapacity(resource.ID, leaseID)

	var updated ProviderResource
	if err := store.db.First(&updated, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.FailureCount != 2 || !updated.Healthy || updated.CooldownUntil != nil {
		t.Fatalf("neutral release changed provider health: %+v", updated)
	}
	var leaseCount int64
	if err := store.db.Model(&InFlightLease{}).Where("id = ?", leaseID).Count(&leaseCount).Error; err != nil {
		t.Fatal(err)
	}
	if leaseCount != 0 {
		t.Fatalf("neutral release left provider lease behind: count=%d", leaseCount)
	}
}
