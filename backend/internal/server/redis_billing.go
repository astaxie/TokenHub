package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisBillingKeyPrefix = "tokenhub:billing:"

var redisBillingAdmitScript = redis.NewScript(`
local now_ms = tonumber(ARGV[1]) or 0
local ttl_ms = tonumber(ARGV[2]) or 60000
local key_rpm = tonumber(ARGV[3]) or 0
local key_tpm = tonumber(ARGV[4]) or 0
local key_reserved = tonumber(ARGV[5]) or 0
local key_concurrency = tonumber(ARGV[6]) or 0
local request_id = ARGV[7]
local user_rpm = tonumber(ARGV[8]) or 0
local user_tpm = tonumber(ARGV[9]) or 0
local user_reserved = tonumber(ARGV[10]) or 0
local user_concurrency = tonumber(ARGV[11]) or 0
local user_lease_id = ARGV[12]
local lease_expires_ms = tonumber(ARGV[13]) or 0

local function current_counter(key)
  return tonumber(redis.call("HGET", key, "requests") or "0"), tonumber(redis.call("HGET", key, "tokens") or "0")
end

local key_requests, key_tokens = current_counter(KEYS[1])
if key_rpm > 0 and key_requests >= key_rpm then
  return {"key_rpm", key_requests, key_tokens, 0, 0}
end
if key_tpm > 0 and key_tokens + key_reserved > key_tpm then
  return {"key_tpm", key_requests, key_tokens, 0, 0}
end

local user_requests = 0
local user_tokens = 0
if user_rpm > 0 or user_tpm > 0 then
  user_requests, user_tokens = current_counter(KEYS[2])
  if user_rpm > 0 and user_requests >= user_rpm then
    return {"user_rpm", key_requests, key_tokens, user_requests, user_tokens}
  end
  if user_tpm > 0 and user_tokens + user_reserved > user_tpm then
    return {"user_tpm", key_requests, key_tokens, user_requests, user_tokens}
  end
end

if key_concurrency > 0 then
  redis.call("ZREMRANGEBYSCORE", KEYS[3], "-inf", now_ms)
  if redis.call("ZCARD", KEYS[3]) >= key_concurrency then
    return {"key_concurrency", key_requests, key_tokens, user_requests, user_tokens}
  end
end
if user_concurrency > 0 then
  redis.call("ZREMRANGEBYSCORE", KEYS[4], "-inf", now_ms)
  if redis.call("ZCARD", KEYS[4]) >= user_concurrency then
    return {"user_concurrency", key_requests, key_tokens, user_requests, user_tokens}
  end
end

if key_rpm > 0 then
  key_requests = redis.call("HINCRBY", KEYS[1], "requests", 1)
end
if key_tpm > 0 and key_reserved > 0 then
  key_tokens = redis.call("HINCRBY", KEYS[1], "tokens", key_reserved)
end
if key_rpm > 0 or key_tpm > 0 then
  redis.call("PEXPIRE", KEYS[1], ttl_ms)
end

if user_rpm > 0 then
  user_requests = redis.call("HINCRBY", KEYS[2], "requests", 1)
end
if user_tpm > 0 and user_reserved > 0 then
  user_tokens = redis.call("HINCRBY", KEYS[2], "tokens", user_reserved)
end
if user_rpm > 0 or user_tpm > 0 then
  redis.call("PEXPIRE", KEYS[2], ttl_ms)
end

if key_concurrency > 0 then
  redis.call("ZADD", KEYS[3], lease_expires_ms, request_id)
  redis.call("PEXPIRE", KEYS[3], math.max(ttl_ms, lease_expires_ms - now_ms))
end
if user_concurrency > 0 then
  redis.call("ZADD", KEYS[4], lease_expires_ms, user_lease_id)
  redis.call("PEXPIRE", KEYS[4], math.max(ttl_ms, lease_expires_ms - now_ms))
end

return {"ok", key_requests, key_tokens, user_requests, user_tokens}
`)

var redisBillingSettleScript = redis.NewScript(`
if redis.call("SET", KEYS[1], "1", "PX", ARGV[1], "NX") == false then
  return 0
end
local key_delta = tonumber(ARGV[2]) or 0
local user_delta = tonumber(ARGV[3]) or 0
local key_lease_id = ARGV[4]
local user_lease_id = ARGV[5]
local lease_ttl_ms = tonumber(ARGV[6]) or 60000
if key_delta ~= 0 then
  redis.call("HINCRBY", KEYS[2], "tokens", key_delta)
end
if user_delta ~= 0 then
  redis.call("HINCRBY", KEYS[3], "tokens", user_delta)
end
redis.call("ZREM", KEYS[4], key_lease_id)
redis.call("ZREM", KEYS[5], user_lease_id)
redis.call("PEXPIRE", KEYS[2], lease_ttl_ms)
redis.call("PEXPIRE", KEYS[3], lease_ttl_ms)
return 1
`)

var redisBillingRollbackScript = redis.NewScript(`
if redis.call("SET", KEYS[1], "1", "PX", ARGV[1], "NX") == false then
  return 0
end
local key_requests = tonumber(ARGV[2]) or 0
local key_tokens = tonumber(ARGV[3]) or 0
local user_requests = tonumber(ARGV[4]) or 0
local user_tokens = tonumber(ARGV[5]) or 0
local key_lease_id = ARGV[6]
local user_lease_id = ARGV[7]
if key_requests ~= 0 then
  redis.call("HINCRBY", KEYS[2], "requests", -key_requests)
end
if key_tokens ~= 0 then
  redis.call("HINCRBY", KEYS[2], "tokens", -key_tokens)
end
if user_requests ~= 0 then
  redis.call("HINCRBY", KEYS[3], "requests", -user_requests)
end
if user_tokens ~= 0 then
  redis.call("HINCRBY", KEYS[3], "tokens", -user_tokens)
end
redis.call("ZREM", KEYS[4], key_lease_id)
redis.call("ZREM", KEYS[5], user_lease_id)
return 1
`)

type redisBillingCoordinator struct {
	client   *redis.Client
	leaseTTL time.Duration
}

type redisBillingAdmitParams struct {
	requestID        string
	keyID            string
	userID           string
	minuteBucket     string
	keyLimits        QuotaLimits
	userLimits       QuotaLimits
	minuteScopes     MinuteLimitScopes
	tokenReservation int64
	now              time.Time
}

type redisBillingAdmitResult struct {
	keyCounter  QuotaCounter
	userCounter QuotaCounter
}

func newRedisBillingCoordinator(ctx context.Context, rawURL string, leaseTTL time.Duration) (*redisBillingCoordinator, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse TOKENHUB_BILLING_REDIS_URL: invalid Redis URL")
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect TOKENHUB_BILLING_REDIS_URL: Redis connection failed")
	}
	return &redisBillingCoordinator{client: client, leaseTTL: effectiveLeaseTTL(leaseTTL, 300*time.Second)}, nil
}

func (c *redisBillingCoordinator) admit(ctx context.Context, params redisBillingAdmitParams) (redisBillingAdmitResult, error) {
	if c == nil {
		return redisBillingAdmitResult{}, nil
	}
	now := params.now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	keyReserved := maxInt64(params.tokenReservation, 0)
	userReserved := int64(0)
	if strings.TrimSpace(params.userID) != "" {
		userReserved = keyReserved
	}
	keys := []string{
		redisBillingMinuteKey(params.keyID, params.minuteBucket),
		redisBillingMinuteKey(userQuotaBucketKey(params.userID), params.minuteBucket),
		redisBillingLeaseKey("api_key", params.keyID),
		redisBillingLeaseKey("user", params.userID),
	}
	ttl := now.Truncate(time.Minute).Add(2 * time.Minute).Sub(now)
	if ttl < time.Minute {
		ttl = time.Minute
	}
	leaseExpires := now.Add(c.leaseTTL)
	values, err := redisBillingAdmitScript.Run(ctx, c.client, keys,
		now.UnixMilli(),
		ttl.Milliseconds(),
		params.keyLimits.RateLimitRPM,
		params.keyLimits.TokenLimitTPM,
		keyReserved,
		params.keyLimits.MaxConcurrency,
		params.requestID,
		params.userLimits.RateLimitRPM,
		params.userLimits.TokenLimitTPM,
		userReserved,
		params.userLimits.MaxConcurrency,
		userConcurrencyLeaseID(params.requestID),
		leaseExpires.UnixMilli(),
	).Slice()
	if err != nil {
		return redisBillingAdmitResult{}, err
	}
	result := redisBillingResult(values)
	counters := redisBillingAdmitResult{
		keyCounter:  QuotaCounter{Requests: result.int64At(1), TotalTokens: result.int64At(2)},
		userCounter: QuotaCounter{Requests: result.int64At(3), TotalTokens: result.int64At(4)},
	}
	switch result.code() {
	case "ok":
		return counters, nil
	case "key_rpm":
		return counters, apiKeyRateLimitError("api_key_rpm_exceeded", "API key requests per minute limit exceeded", params.minuteScopes.RPM, params.keyLimits, counters.keyCounter, now)
	case "key_tpm":
		return counters, apiKeyRateLimitError("api_key_tpm_exceeded", "API key tokens per minute limit exceeded", params.minuteScopes.TPM, params.keyLimits, counters.keyCounter, now)
	case "key_concurrency":
		return counters, ErrRateLimitExceeded
	case "user_rpm", "user_tpm", "user_concurrency":
		err := quotaExceededError("user")
		err.Headers = combinedRateLimitHeaders(params.keyLimits, counters.keyCounter, params.userLimits, counters.userCounter, now, true)
		return counters, err
	default:
		return counters, fmt.Errorf("unexpected Redis billing admission result %q", result.code())
	}
}

func (c *redisBillingCoordinator) settle(ctx context.Context, call CallContext, actualTokens int64) error {
	if c == nil || !call.RedisBillingAdmitted || strings.TrimSpace(call.RequestID) == "" {
		return nil
	}
	delta := maxInt64(actualTokens, 0) - maxInt64(call.ReservedTokens, 0)
	keyDelta := int64(0)
	if strings.TrimSpace(call.TokenLimitBucket) != "" {
		keyDelta = delta
	}
	keys := []string{
		redisBillingSettledKey(call.RequestID),
		redisBillingMinuteKey(call.Key.ID, call.TokenLimitBucket),
		redisBillingMinuteKey(call.UserQuotaID, call.UserTokenLimitBucket),
		redisBillingLeaseKey("api_key", call.Key.ID),
		redisBillingLeaseKey("user", call.AttributedUserID),
	}
	userDelta := int64(0)
	if strings.TrimSpace(call.UserTokenLimitBucket) != "" {
		userDelta = delta
	}
	if err := redisBillingSettleScript.Run(ctx, c.client, keys,
		(24 * time.Hour).Milliseconds(),
		keyDelta,
		userDelta,
		call.RequestID,
		userConcurrencyLeaseID(call.RequestID),
		c.leaseTTL.Milliseconds(),
	).Err(); err != nil {
		return fmt.Errorf("settle Redis billing reservation request=%s: %w", call.RequestID, err)
	}
	return nil
}

func (c *redisBillingCoordinator) rollback(ctx context.Context, call CallContext) error {
	if c == nil || !call.RedisBillingAdmitted || strings.TrimSpace(call.RequestID) == "" {
		return nil
	}
	keyRequests := int64(0)
	if call.MinuteRequestHeld {
		keyRequests = 1
	}
	userRequests := int64(0)
	if call.UserMinuteRequestHeld {
		userRequests = 1
	}
	keyTokens := int64(0)
	if strings.TrimSpace(call.TokenLimitBucket) != "" {
		keyTokens = maxInt64(call.ReservedTokens, 0)
	}
	userTokens := int64(0)
	if strings.TrimSpace(call.UserTokenLimitBucket) != "" {
		userTokens = maxInt64(call.ReservedTokens, 0)
	}
	if err := redisBillingRollbackScript.Run(ctx, c.client, []string{
		redisBillingSettledKey(call.RequestID),
		redisBillingMinuteKey(call.Key.ID, call.TokenLimitBucket),
		redisBillingMinuteKey(call.UserQuotaID, call.UserTokenLimitBucket),
		redisBillingLeaseKey("api_key", call.Key.ID),
		redisBillingLeaseKey("user", call.AttributedUserID),
	},
		(24 * time.Hour).Milliseconds(),
		keyRequests,
		keyTokens,
		userRequests,
		userTokens,
		call.RequestID,
		userConcurrencyLeaseID(call.RequestID),
	).Err(); err != nil {
		return fmt.Errorf("rollback Redis billing reservation request=%s: %w", call.RequestID, err)
	}
	return nil
}

func (c *redisBillingCoordinator) renewLease(ctx context.Context, scopeType string, scopeID string, leaseID string) (time.Duration, bool, error) {
	if c == nil || strings.TrimSpace(leaseID) == "" {
		return 0, false, nil
	}
	expiresAt := time.Now().UTC().Add(c.leaseTTL)
	key := redisBillingLeaseKey(scopeType, scopeID)
	updated, err := c.client.ZAddXX(ctx, key, redis.Z{Score: float64(expiresAt.UnixMilli()), Member: leaseID}).Result()
	if err != nil || updated == 0 {
		return 0, false, err
	}
	_ = c.client.PExpire(ctx, key, c.leaseTTL).Err()
	return c.leaseTTL, true, nil
}

func redisBillingMinuteKey(scopeID string, bucket string) string {
	return redisBillingKeyPrefix + "minute:" + strings.TrimSpace(scopeID) + ":" + strings.TrimSpace(bucket)
}

func redisBillingLeaseKey(scopeType string, scopeID string) string {
	return redisBillingKeyPrefix + "lease:" + strings.TrimSpace(scopeType) + ":" + strings.TrimSpace(scopeID)
}

func redisBillingSettledKey(requestID string) string {
	return redisBillingKeyPrefix + "settled:" + strings.TrimSpace(requestID)
}

type redisBillingResult []any

func (r redisBillingResult) code() string {
	if len(r) == 0 {
		return ""
	}
	value, _ := r[0].(string)
	return value
}

func (r redisBillingResult) int64At(index int) int64 {
	if index >= len(r) {
		return 0
	}
	switch value := r[index].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(value, 10, 64)
		return parsed
	default:
		return 0
	}
}
