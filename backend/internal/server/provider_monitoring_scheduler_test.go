package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProviderMonitoringSchedulerRunsOncePerRevision(t *testing.T) {
	store := NewMemoryStore()
	runs := 0
	scheduler := newProviderMonitoringScheduler(store, func(context.Context) error {
		runs++
		return nil
	})
	base := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)

	if err := scheduler.RunDue(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.RunDue(context.Background(), base.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("same scheduler revision ran %d times, want 1", runs)
	}
	if err := scheduler.RunDue(context.Background(), base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("next scheduler revision ran %d times, want 2", runs)
	}
}

func TestProviderMonitoringSchedulerRetriesFailedRevision(t *testing.T) {
	store := NewMemoryStore()
	runs := 0
	scheduler := newProviderMonitoringScheduler(store, func(context.Context) error {
		runs++
		if runs == 1 {
			return errors.New("transient probe failure")
		}
		return nil
	})
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)

	if err := scheduler.RunDue(context.Background(), now); err == nil {
		t.Fatal("first scheduler run unexpectedly succeeded")
	}
	if err := scheduler.RunDue(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		t.Fatalf("failed scheduler revision ran %d times, want retry", runs)
	}
}
