package server

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type callAdmissionResult struct {
	call              CallContext
	leaseAcquired     bool
	leaseConfirmedFor time.Duration
}

// admitCallTransaction performs the complete quota and concurrency admission
// inside the caller's transaction. Durable background jobs use this helper in
// the same transaction that records their admitted phase, closing the crash
// window between consuming quota and persisting the request ID.
func (s *GormStore) admitCallTransaction(ctx context.Context, tx *gorm.DB, key APIKey, modelName string, tokenReservation int64, requestID string) (callAdmissionResult, error) {
	var admission callAdmissionResult
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
	policyLimits, minuteLimitScopes, err := quotaPolicyLimits(tx, privateProject, privateKey)
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
	now, err := s.databaseNow(tx)
	if err != nil {
		return admission, err
	}
	measuredAt := time.Now()
	if err := pruneAPIKeyMinuteBuckets(tx, privateKey.ID, now); err != nil {
		return admission, err
	}
	minuteCounter, err := s.consumeAPIKeyMinuteRequest(tx, privateKey.ID, effectiveLimits, minuteLimitScopes, tokenReservation, now)
	if err != nil {
		return admission, err
	}
	dayCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "day", dayBucket(now))
	if err != nil {
		return admission, err
	}
	monthCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "month", monthBucket(now))
	if err != nil {
		return admission, err
	}
	if effectiveLimits.MaxConcurrency > 0 {
		confirmedFor, err := s.acquireInFlightLease(tx, "api_key", privateKey.ID, effectiveLimits.MaxConcurrency, requestID)
		if err != nil {
			return admission, err
		}
		admission.leaseConfirmedFor = confirmedFor
		admission.leaseAcquired = true
	}
	if exceedsRequestQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
		exceedsTokenQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
		exceedsCostQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) {
		return admission, ErrQuotaExceeded
	}
	if err := s.checkRuntimeBudget(tx, privateProject, now); err != nil {
		return admission, err
	}
	dayCounter.Requests++
	monthCounter.Requests++
	if err := tx.Save(&dayCounter).Error; err != nil {
		return admission, err
	}
	if err := tx.Save(&monthCounter).Error; err != nil {
		return admission, err
	}
	admission.call = CallContext{
		RequestID:        requestID,
		Project:          privateProject,
		Key:              publicKey(privateKey),
		Model:            model,
		StartedAt:        now,
		measuredAt:       measuredAt,
		RateLimitHeaders: apiKeyRateLimitHeaders(effectiveLimits, minuteCounter, now, false),
		requestContext:   ctx,
	}
	if effectiveLimits.RateLimitRPM > 0 {
		admission.call.MinuteRequestHeld = true
	}
	if effectiveLimits.TokenLimitTPM > 0 {
		admission.call.TokenLimitBucket = minuteBucket(now)
		admission.call.ReservedTokens = maxInt64(tokenReservation, 0)
	}
	return admission, nil
}

func (s *GormStore) startAdmittedCallHeartbeat(ctx context.Context, admission callAdmissionResult) CallContext {
	call := admission.call
	if admission.leaseAcquired {
		call.requestContext = s.startInFlightLeaseHeartbeat(ctx, call.RequestID, admission.leaseConfirmedFor)
	}
	return call
}
