package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisLeaseTestCoordinator(t *testing.T) (*redisBillingCoordinator, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	coordinator, err := newRedisBillingCoordinator(context.Background(), "redis://"+server.Addr(), 600*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.client.Close() })
	return coordinator, server
}

func TestRedisBillingRenewLeaseRetainsExistingMember(t *testing.T) {
	for _, scope := range []string{"api_key", "user"} {
		t.Run(scope, func(t *testing.T) {
			coordinator, server := newRedisLeaseTestCoordinator(t)
			ctx := context.Background()
			key := redisBillingLeaseKey(scope, "scope")
			initialScore := float64(time.Now().Add(100 * time.Millisecond).UnixMilli())
			if err := coordinator.client.ZAdd(ctx, key, redis.Z{Score: initialScore, Member: "lease"}).Err(); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.client.PExpire(ctx, key, coordinator.leaseTTL).Err(); err != nil {
				t.Fatal(err)
			}
			server.FastForward(200 * time.Millisecond)
			ttl, retained, err := coordinator.renewLease(ctx, scope, "scope", "lease")
			if err != nil || !retained || ttl != coordinator.leaseTTL {
				t.Fatalf("existing lease was not renewed: ttl=%v retained=%v err=%v", ttl, retained, err)
			}
			score, err := coordinator.client.ZScore(ctx, key, "lease").Result()
			if err != nil || score <= initialScore {
				t.Fatalf("lease expiration did not advance: score=%v initial=%v err=%v", score, initialScore, err)
			}
			if got := server.TTL(key); got != coordinator.leaseTTL {
				t.Fatalf("lease key TTL was not refreshed: got %v, want %v", got, coordinator.leaseTTL)
			}
		})
	}
}

func TestRedisBillingRenewLeaseDoesNotRecreateMissingMember(t *testing.T) {
	for _, scenario := range []string{"missing_key", "missing_member", "expired_key"} {
		t.Run(scenario, func(t *testing.T) {
			coordinator, server := newRedisLeaseTestCoordinator(t)
			ctx := context.Background()
			key := redisBillingLeaseKey("api_key", "scope")
			if scenario != "missing_key" {
				member := "other"
				if scenario == "expired_key" {
					member = "lease"
				}
				if err := coordinator.client.ZAdd(ctx, key, redis.Z{Score: 1, Member: member}).Err(); err != nil {
					t.Fatal(err)
				}
				if err := coordinator.client.PExpire(ctx, key, coordinator.leaseTTL).Err(); err != nil {
					t.Fatal(err)
				}
				if scenario == "expired_key" {
					server.FastForward(coordinator.leaseTTL)
				}
			}
			beforeTTL := server.TTL(key)
			ttl, retained, err := coordinator.renewLease(ctx, "api_key", "scope", "lease")
			if err != nil || retained || ttl != 0 {
				t.Fatalf("missing lease was retained: ttl=%v retained=%v err=%v", ttl, retained, err)
			}
			if err := coordinator.client.ZScore(ctx, key, "lease").Err(); !errors.Is(err, redis.Nil) {
				t.Fatalf("missing lease was recreated: %v", err)
			}
			if got := server.TTL(key); got != beforeTTL {
				t.Fatalf("missing lease changed the key TTL: got %v, want %v", got, beforeTTL)
			}
		})
	}
}

func TestRedisBillingRenewLeasePropagatesRedisError(t *testing.T) {
	coordinator, server := newRedisLeaseTestCoordinator(t)
	if err := server.Set(redisBillingLeaseKey("api_key", "scope"), "wrong-type"); err != nil {
		t.Fatal(err)
	}
	ttl, retained, err := coordinator.renewLease(context.Background(), "api_key", "scope", "lease")
	if err == nil || retained || ttl != 0 {
		t.Fatalf("Redis error was not propagated: ttl=%v retained=%v err=%v", ttl, retained, err)
	}
}

func TestRedisBillingLeaseHeartbeatRetainsThenLosesLease(t *testing.T) {
	coordinator, _ := newRedisLeaseTestCoordinator(t)
	coordinator.leaseTTL = 3 * time.Second
	ctx := context.Background()
	key := redisBillingLeaseKey("api_key", "scope")
	if err := coordinator.client.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Add(coordinator.leaseTTL).UnixMilli()), Member: "lease"}).Err(); err != nil {
		t.Fatal(err)
	}
	renewals := make(chan struct{}, 2)
	heartbeat := startLeaseHeartbeat(ctx, coordinator.leaseTTL, coordinator.leaseTTL, func(ctx context.Context) (time.Duration, bool, error) {
		ttl, retained, err := coordinator.renewLease(ctx, "api_key", "scope", "lease")
		if retained && err == nil {
			select {
			case renewals <- struct{}{}:
			default:
			}
		}
		return ttl, retained, err
	})
	t.Cleanup(func() { _ = stopLeaseHeartbeat(heartbeat) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for range 2 {
		select {
		case <-heartbeat.ctx.Done():
			t.Fatalf("healthy lease canceled request: %v", context.Cause(heartbeat.ctx))
		case <-renewals:
		case <-deadline.C:
			t.Fatal("timed out waiting for lease renewals")
		}
	}
	if err := coordinator.client.ZRem(ctx, key, "lease").Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-heartbeat.ctx.Done():
		if !errors.Is(context.Cause(heartbeat.ctx), ErrCoordinationLeaseLost) {
			t.Fatalf("unexpected cancellation cause: %v", context.Cause(heartbeat.ctx))
		}
	case <-deadline.C:
		t.Fatal("missing lease did not cancel the request")
	}
}

func TestRedisBillingRenewLeaseWithUnchangedExpiration(t *testing.T) {
	coordinator, server := newRedisLeaseTestCoordinator(t)
	ctx := context.Background()
	key := redisBillingLeaseKey("api_key", "scope")
	expiresAt := time.Now().Add(coordinator.leaseTTL).UnixMilli()
	if err := coordinator.client.ZAdd(ctx, key, redis.Z{Score: float64(expiresAt), Member: "lease"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.client.PExpire(ctx, key, coordinator.leaseTTL).Err(); err != nil {
		t.Fatal(err)
	}
	server.FastForward(200 * time.Millisecond)
	retained, err := redisBillingRenewLeaseScript.Run(ctx, coordinator.client, []string{key}, "lease", expiresAt, coordinator.leaseTTL.Milliseconds()).Int64()
	if err != nil || retained != 1 {
		t.Fatalf("unchanged expiration was treated as a lost lease: retained=%d err=%v", retained, err)
	}
	if got := server.TTL(key); got != coordinator.leaseTTL {
		t.Fatalf("unchanged expiration did not refresh TTL: got %v, want %v", got, coordinator.leaseTTL)
	}
}
