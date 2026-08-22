package server

import (
	"context"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

type callAdmissionResult struct {
	call                  CallContext
	leaseAcquired         bool
	leaseConfirmedFor     time.Duration
	userLeaseAcquired     bool
	userLeaseConfirmedFor time.Duration
}

// admitCallTransaction performs the complete quota and concurrency admission
// inside the caller's transaction. Durable background jobs use this helper in
// the same transaction that records their admitted phase, closing the crash
// window between consuming quota and persisting the request ID.
func (s *GormStore) admitCallTransaction(ctx context.Context, tx *gorm.DB, key APIKey, modelName string, tokenReservation int64, requestID string) (admission callAdmissionResult, err error) {
	redisAdmitted := false
	defer func() {
		if err != nil && redisAdmitted {
			if rollbackErr := s.billingRedis.rollback(ctx, admission.call); rollbackErr != nil {
				log.Printf("[tokenhub] failed to rollback Redis billing admission request=%s: %v", requestID, rollbackErr)
			}
		}
	}()
	if err := s.lockScopeForUpdate(tx, "api_key", key.ID); err != nil {
		return admission, err
	}
	var privateKey APIKey
	if err := tx.First(&privateKey, "id = ?", key.ID).Error; err != nil {
		return admission, ErrInvalidAPIKey
	}
	hydrateAPIKey(&privateKey)
	if err := s.lockScopeForSharedRead(tx, "project", privateKey.ProjectID); err != nil {
		return admission, err
	}
	var privateProject Project
	if err := tx.First(&privateProject, "id = ?", privateKey.ProjectID).Error; err != nil {
		return admission, ErrInvalidAPIKey
	}
	hydrateProject(&privateProject)
	authorizationNow := time.Now().UTC()
	if privateProject.Status != StatusActive || privateKey.Status == StatusDisabled ||
		(privateKey.Status == StatusRevoked && (privateKey.GraceUntil == nil || !authorizationNow.Before(*privateKey.GraceUntil))) {
		return admission, ErrAPIKeyDisabled
	}
	if privateKey.ExpiresAt != nil && authorizationNow.After(*privateKey.ExpiresAt) {
		return admission, ErrAPIKeyExpired
	}
	var model Model
	if err := tx.First(&model, "name = ? AND status = ?", modelName, StatusActive).Error; err != nil {
		return admission, ErrModelNotAllowed
	}
	if !modelAllowedByScopes(privateProject, privateKey, modelName) {
		return admission, ErrModelNotAllowed
	}
	keyLimits := privateKey.Limits
	keyLimits.RateLimitRPM = 0
	keyLimits.TokenLimitTPM = 0
	if privateKey.RateLimitRPM != nil {
		keyLimits.RateLimitRPM = *privateKey.RateLimitRPM
	}
	if privateKey.TokenLimitTPM != nil {
		keyLimits.TokenLimitTPM = *privateKey.TokenLimitTPM
	}
	policyLimits, minuteLimitScopes, userPolicy, err := quotaPolicyLimits(tx, privateProject, privateKey)
	if err != nil {
		return admission, err
	}
	if strictLimitChanged(policyLimits.RateLimitRPM, keyLimits.RateLimitRPM) {
		minuteLimitScopes.RPM = "api_key"
	}
	if strictLimitChanged(policyLimits.TokenLimitTPM, keyLimits.TokenLimitTPM) {
		minuteLimitScopes.TPM = "api_key"
	}
	effectiveLimits := mergeQuotaLimits(keyLimits, policyLimits)
	if strictLimitChanged(policyLimits.DailyRequests, keyLimits.DailyRequests) {
		minuteLimitScopes.DailyRequests = "api_key"
	}
	if strictLimitChanged(policyLimits.MonthlyRequests, keyLimits.MonthlyRequests) {
		minuteLimitScopes.MonthlyRequests = "api_key"
	}
	if strictLimitChanged(policyLimits.DailyTokens, keyLimits.DailyTokens) {
		minuteLimitScopes.DailyTokens = "api_key"
	}
	if strictLimitChanged(policyLimits.MonthlyTokens, keyLimits.MonthlyTokens) {
		minuteLimitScopes.MonthlyTokens = "api_key"
	}
	if policyLimits.DailyCostUSD <= 0 && keyLimits.DailyCostUSD > 0 || keyLimits.DailyCostUSD > 0 && keyLimits.DailyCostUSD < policyLimits.DailyCostUSD {
		minuteLimitScopes.DailyCostUSD = "api_key"
	}
	if policyLimits.MonthlyCostUSD <= 0 && keyLimits.MonthlyCostUSD > 0 || keyLimits.MonthlyCostUSD > 0 && keyLimits.MonthlyCostUSD < policyLimits.MonthlyCostUSD {
		minuteLimitScopes.MonthlyCostUSD = "api_key"
	}
	fillMissingKeyQuotaLimitScopes(&minuteLimitScopes, effectiveLimits)
	now, err := s.databaseNow(tx)
	if err != nil {
		return admission, err
	}
	measuredAt := time.Now()
	attributedUserID := usageAttributionUserID(privateKey, privateProject)
	minuteCounter := QuotaCounter{}
	userMinuteCounter := QuotaCounter{}
	userQuotaID := ""
	if s.billingRedis == nil {
		if err := pruneAPIKeyMinuteBuckets(tx, privateKey.ID, now); err != nil {
			return admission, err
		}
		minuteCounter, err = s.consumeAPIKeyMinuteRequest(tx, privateKey.ID, effectiveLimits, minuteLimitScopes, tokenReservation, now)
		if err != nil {
			return admission, err
		}
	}
	if userPolicy.Enabled() {
		userQuotaID = userQuotaBucketKey(userPolicy.UserID)
		if err := s.lockScopeForUpdate(tx, "user_quota", userQuotaID); err != nil {
			return admission, err
		}
		// The user aggregate has its own minute bucket. Use only the user policy
		// limits here: an API-key-specific limit must not become an aggregate
		// limit for every key attributed to the same user.
		userMinuteScopes := MinuteLimitScopes{}
		if userPolicy.Limits.RateLimitRPM > 0 {
			userMinuteScopes.RPM = "user"
		}
		if userPolicy.Limits.TokenLimitTPM > 0 {
			userMinuteScopes.TPM = "user"
		}
		if s.billingRedis == nil {
			userMinuteCounter, err = s.consumeAPIKeyMinuteRequest(tx, userQuotaID, userPolicy.Limits, userMinuteScopes, tokenReservation, now, userPolicy.UserID)
			if err != nil {
				httpErr := AsHTTPError(err)
				if httpErr.Code == "api_key_rpm_exceeded" || httpErr.Code == "api_key_tpm_exceeded" {
					quotaErr := scopedHTTPError(ErrQuotaExceeded, "user")
					quotaErr.Headers = httpErr.Headers
					return admission, quotaErr
				}
				return admission, err
			}
		}
	}
	dayCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "day", dayBucket(now))
	if err != nil {
		return admission, err
	}
	userDayCounter := QuotaBucket{}
	userMonthCounter := QuotaBucket{}
	if userPolicy.Enabled() {
		userDayCounter, err = s.quotaBucketForUpdate(tx, userQuotaID, "day", dayBucket(now), userPolicy.UserID)
		if err != nil {
			return admission, err
		}
		userMonthCounter, err = s.quotaBucketForUpdate(tx, userQuotaID, "month", monthBucket(now), userPolicy.UserID)
		if err != nil {
			return admission, err
		}
		historicalDay, err := s.aggregateUserQuotaCounter(tx, userPolicy.UserID, "day", dayBucket(now))
		if err != nil {
			return admission, err
		}
		historicalMonth, err := s.aggregateUserQuotaCounter(tx, userPolicy.UserID, "month", monthBucket(now))
		if err != nil {
			return admission, err
		}
		mergeQuotaCounterMax(&userDayCounter.QuotaCounter, historicalDay)
		mergeQuotaCounterMax(&userMonthCounter.QuotaCounter, historicalMonth)
	}
	monthCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "month", monthBucket(now))
	if err != nil {
		return admission, err
	}
	if s.billingRedis == nil && effectiveLimits.MaxConcurrency > 0 {
		confirmedFor, err := s.acquireInFlightLease(tx, "api_key", privateKey.ID, effectiveLimits.MaxConcurrency, requestID)
		if err != nil {
			return admission, err
		}
		admission.leaseConfirmedFor = confirmedFor
		admission.leaseAcquired = true
	}
	if s.billingRedis == nil && userPolicy.Enabled() && userPolicy.Limits.MaxConcurrency > 0 {
		confirmedFor, err := s.acquireInFlightLease(tx, "user", userPolicy.UserID, userPolicy.Limits.MaxConcurrency, userConcurrencyLeaseID(requestID))
		if err != nil {
			if AsHTTPError(err).Code == ErrRateLimitExceeded.Code {
				return admission, quotaExceededError("user")
			}
			return admission, err
		}
		admission.userLeaseConfirmedFor = confirmedFor
		admission.userLeaseAcquired = true
	}
	if userPolicy.Enabled() {
		if limit := exceededQuotaLimitWithReservation(userPolicy.Limits, &userDayCounter.QuotaCounter, &userMonthCounter.QuotaCounter, tokenReservation); limit != "" {
			s.metrics.ObserveRateLimitHit(userQuotaID, limit, "user")
			return admission, quotaExceededError("user")
		}
	}
	if limit, scope := exceededQuotaLimitWithScope(effectiveLimits, minuteLimitScopes, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter); limit != "" {
		s.metrics.ObserveRateLimitHit(privateKey.ID, limit, scope)
		return admission, quotaExceededError(scope)
	}
	if err := s.checkRuntimeBudget(tx, privateProject, now); err != nil {
		return admission, err
	}
	admission.call = CallContext{
		RequestID:             requestID,
		Project:               privateProject,
		Key:                   publicKey(privateKey),
		Model:                 model,
		StartedAt:             now,
		measuredAt:            measuredAt,
		requestContext:        ctx,
		UserQuotaID:           userQuotaID,
		UserQuotaEnabled:      userPolicy.Enabled(),
		UserMinuteRequestHeld: userPolicy.Enabled() && userPolicy.Limits.RateLimitRPM > 0,
		UserQuotaLimits:       userPolicy.Limits,
		AttributedUserID:      attributedUserID,
		RedisKeyLeaseHeld:     s.billingRedis != nil && effectiveLimits.MaxConcurrency > 0,
		RedisUserLeaseHeld:    s.billingRedis != nil && userPolicy.Enabled() && userPolicy.Limits.MaxConcurrency > 0,
	}
	if effectiveLimits.RateLimitRPM > 0 {
		admission.call.MinuteRequestHeld = true
	}
	if effectiveLimits.TokenLimitTPM > 0 {
		admission.call.TokenLimitBucket = minuteBucket(now)
	}
	if userPolicy.Enabled() && userPolicy.Limits.TokenLimitTPM > 0 {
		admission.call.UserTokenLimitBucket = minuteBucket(now)
	}
	if effectiveLimits.TokenLimitTPM > 0 || userPolicy.Enabled() {
		admission.call.ReservedTokens = maxInt64(tokenReservation, 0)
	}
	if s.billingRedis != nil {
		redisCounters, redisErr := s.billingRedis.admit(ctx, redisBillingAdmitParams{
			requestID:        requestID,
			keyID:            privateKey.ID,
			userID:           userPolicy.UserID,
			minuteBucket:     minuteBucket(now),
			keyLimits:        effectiveLimits,
			userLimits:       userPolicy.Limits,
			minuteScopes:     minuteLimitScopes,
			tokenReservation: tokenReservation,
			now:              now,
		})
		if redisErr != nil {
			return admission, redisErr
		}
		redisAdmitted = true
		admission.call.RedisBillingAdmitted = true
		minuteCounter = redisCounters.keyCounter
		userMinuteCounter = redisCounters.userCounter
	}
	dayCounter.Requests++
	monthCounter.Requests++
	if err := tx.Save(&dayCounter).Error; err != nil {
		return admission, err
	}
	if err := tx.Save(&monthCounter).Error; err != nil {
		return admission, err
	}
	if userPolicy.Enabled() {
		userDayCounter.Requests++
		userMonthCounter.Requests++
		reservation := maxInt64(tokenReservation, 0)
		userDayCounter.TotalTokens = saturatingAddNonNegative(userDayCounter.TotalTokens, reservation)
		userMonthCounter.TotalTokens = saturatingAddNonNegative(userMonthCounter.TotalTokens, reservation)
		if err := tx.Save(&userDayCounter).Error; err != nil {
			return admission, err
		}
		if err := tx.Save(&userMonthCounter).Error; err != nil {
			return admission, err
		}
	}
	admission.call.RateLimitHeaders = apiKeyRateLimitHeaders(effectiveLimits, minuteCounter, now, false)
	if userPolicy.Enabled() {
		admission.call.RateLimitHeaders = combinedRateLimitHeaders(effectiveLimits, minuteCounter, userPolicy.Limits, userMinuteCounter, now, false)
	}
	if effectiveLimits.RateLimitRPM > 0 {
		admission.call.MinuteRequestHeld = true
	}
	if effectiveLimits.TokenLimitTPM > 0 {
		admission.call.TokenLimitBucket = minuteBucket(now)
	}
	if userPolicy.Enabled() && userPolicy.Limits.TokenLimitTPM > 0 {
		admission.call.UserTokenLimitBucket = minuteBucket(now)
	}
	if effectiveLimits.TokenLimitTPM > 0 || userPolicy.Enabled() {
		admission.call.ReservedTokens = maxInt64(tokenReservation, 0)
	}
	return admission, nil
}

func (s *GormStore) startAdmittedCallHeartbeat(ctx context.Context, admission callAdmissionResult) CallContext {
	call := admission.call
	if call.RedisBillingAdmitted {
		if call.RedisKeyLeaseHeld {
			call.requestContext = s.startRedisBillingLeaseHeartbeat(ctx, "api_key", call.Key.ID, call.RequestID)
		}
		if call.RedisUserLeaseHeld {
			call.requestContext = s.startRedisBillingLeaseHeartbeat(call.requestContext, "user", call.AttributedUserID, userConcurrencyLeaseID(call.RequestID))
		}
		return call
	}
	if admission.leaseAcquired {
		call.requestContext = s.startInFlightLeaseHeartbeat(ctx, call.RequestID, admission.leaseConfirmedFor)
	}
	if admission.userLeaseAcquired {
		call.requestContext = s.startInFlightLeaseHeartbeat(call.requestContext, userConcurrencyLeaseID(call.RequestID), admission.userLeaseConfirmedFor)
	}
	return call
}

func (s *GormStore) startRedisBillingLeaseHeartbeat(parent context.Context, scopeType string, scopeID string, leaseID string) context.Context {
	if s.billingRedis == nil || strings.TrimSpace(leaseID) == "" {
		return parent
	}
	ttl := effectiveLeaseTTL(s.inFlightLeaseTTL, 300*time.Second)
	heartbeat := startLeaseHeartbeat(parent, ttl, ttl, func(attemptCtx context.Context) (time.Duration, bool, error) {
		return s.billingRedis.renewLease(attemptCtx, scopeType, scopeID, leaseID)
	})
	if previous, loaded := s.leaseHeartbeats.LoadOrStore(leaseID, heartbeat); loaded {
		if previousHeartbeat, ok := previous.(*leaseHeartbeat); ok {
			_ = stopLeaseHeartbeat(previousHeartbeat)
		}
		s.leaseHeartbeats.Store(leaseID, heartbeat)
	}
	return heartbeat.ctx
}
