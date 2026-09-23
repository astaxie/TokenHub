package server

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type halfOpenClaimKey struct{}

// withHalfOpenClaim marks a call context as belonging to the request that won the
// half-open trial for a resource.
//
// The marker is what makes recovery safe. Without it, any in-flight request that
// happens to succeed after the breaker opened would close it: requests A and B start
// while the resource is healthy, A fails and trips the breaker, then B returns
// successfully and finds the resource unhealthy. B never held a trial permit and its
// success says nothing about the state that tripped the breaker, so it must not
// recover the resource. Only the claimant may.
//
// Carrying this in the context rather than a column is safe because the claim and the
// completion always happen inside the same request on the same replica. It also
// survives MaxConcurrency == 0, where no lease ID is issued at all.
func withHalfOpenClaim(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, halfOpenClaimKey{}, true)
}

func hasHalfOpenClaim(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	claimed, _ := ctx.Value(halfOpenClaimKey{}).(bool)
	return claimed
}

// Provider resource breaker recovery.
//
// A resource that trips the failure threshold is parked with healthy = false and a
// cooldown deadline. Recovery is half-open and rides on real traffic rather than a
// background prober: once the deadline lapses the resource is admitted back into the
// candidate pool, the first request to reach capacity admission claims the trial by
// pushing the deadline forward, and a confirmed upstream success closes the breaker.
//
// Pushing cooldown_until forward is the whole mechanism. Because it happens inside the
// admission transaction it is simultaneously the concurrency limit (one trial in
// flight per resource), the failure backoff (a failed trial has already armed the next
// window) and the cross-replica guard (any replica losing the compare-and-set is
// rejected). No extra column, scheduler or lock is involved.

// maxCooldownSeconds bounds the configured cooldown values. Without it a hostile or
// fat-fingered environment variable would overflow the seconds-to-Duration multiply
// and wrap into a zero or negative window, disabling the breaker entirely.
const maxCooldownSeconds = 365 * 24 * 60 * 60

func cooldownSecondsToDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if seconds > maxCooldownSeconds {
		seconds = maxCooldownSeconds
	}
	return time.Duration(seconds) * time.Second
}

// halfOpenEligible reports whether a resource may be offered as a routing candidate:
// either it is healthy, or its cooldown has lapsed so it deserves one trial request.
// Callers must still check status separately — an admin-disabled resource is never
// eligible regardless of its breaker state.
func halfOpenEligible(resource ProviderResource, now time.Time) bool {
	if resource.Healthy {
		return true
	}
	return resource.CooldownUntil != nil && !now.Before(*resource.CooldownUntil)
}

// cooldownWindow returns how long a resource should stay parked after reaching
// failureCount consecutive failures. The window doubles per failure beyond the
// threshold and saturates at cooldownMax.
//
// Doubling is done by repeated addition with an early return once the value passes
// half the cap, so neither a shift count nor a time.Duration can overflow no matter
// how large failureCount grows.
func (s *GormStore) cooldownWindow(failureCount int) time.Duration {
	base := s.cooldownDuration
	if base <= 0 {
		return 0
	}
	max := s.cooldownMax
	if max <= 0 || max < base {
		return base
	}
	window := base
	for i := s.failureThreshold; i < failureCount; i++ {
		if window > max/2 {
			return max
		}
		window *= 2
	}
	return window
}

// nextFailureCount increments the consecutive failure counter, saturating once the
// counter can no longer widen the cooldown window. Without the clamp a permanently
// broken resource would grow the counter without bound for no added effect.
// The comparison precedes the increment so a corrupt or absurd stored counter cannot
// overflow into a negative value.
func (s *GormStore) nextFailureCount(current int) int {
	ceiling := s.failureCountCeiling()
	if current >= ceiling {
		return ceiling
	}
	return current + 1
}

// failureCountCeiling is the smallest failure count whose window already reaches
// cooldownMax. Counting past it changes nothing, so the stored counter stops there.
func (s *GormStore) failureCountCeiling() int {
	ceiling := s.failureThreshold
	if ceiling < 1 {
		ceiling = 1
	}
	base := s.cooldownDuration
	max := s.cooldownMax
	if base <= 0 || max <= 0 || max <= base {
		return ceiling
	}
	for window := base; window <= max/2; window *= 2 {
		ceiling++
	}
	return ceiling
}

// RecoverProviderResource clears the breaker for a resource whose health has been
// confirmed out of band — today that is a probe which actually reached the upstream.
//
// It refuses to recover a resource an admin disabled: status is the operator's switch
// and must outrank any automatic signal.
func (s *GormStore) RecoverProviderResource(resourceID string) (ProviderResource, error) {
	if resourceID == "" {
		return ProviderResource{}, NewHTTPError(404, "provider_resource_not_found", "Provider resource not found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var recovered ProviderResource
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var resource ProviderResource
		if err := tx.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		now := time.Now().UTC()
		if resource.Status != StatusActive {
			recovered = resource
			return nil
		}
		alreadyHealthy := resource.Healthy
		updates := map[string]any{
			"healthy":         true,
			"failure_count":   0,
			"cooldown_until":  nil,
			"last_checked_at": now,
			"updated_at":      now,
		}
		// Re-check status inside the UPDATE rather than trusting the earlier read: on
		// a multi-replica deployment an admin can disable the resource in between, and
		// an unconditional write would silently flip it back to healthy.
		result := tx.Model(&ProviderResource{}).
			Where("id = ? AND status = ?", resourceID, StatusActive).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			recovered = resource
			return nil
		}
		if !alreadyHealthy {
			if err := tx.Create(&AlertEvent{
				ID:         NewID("alt"),
				ScopeType:  "provider_resource",
				ScopeID:    resource.ID,
				Severity:   "info",
				Code:       "provider_resource_recovered",
				Message:    "Provider resource recovered after a successful probe",
				ResourceID: resource.ProviderID,
				CreatedAt:  now,
			}).Error; err != nil {
				return err
			}
		}
		resource.Healthy = true
		resource.FailureCount = 0
		resource.CooldownUntil = nil
		resource.LastCheckedAt = &now
		resource.UpdatedAt = now
		recovered = resource
		return nil
	})
	if err != nil {
		return ProviderResource{}, err
	}
	s.maskProviderResourceHeaderConfig(&recovered)
	redactProviderResourceSecrets(&recovered)
	return recovered, nil
}
