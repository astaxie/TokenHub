package server

import "time"

func quotaActualTokens(call CallContext, usage Usage) int64 {
	providerTokens := meteredTokens(usage)
	actualTokens := usage.RateLimitTokens
	if actualTokens <= 0 {
		actualTokens = providerTokens
	}
	if call.StreamOutputCommitted && providerTokens == 0 && actualTokens < call.ReservedTokens {
		actualTokens = call.ReservedTokens
	}
	return actualTokens
}

func imageJobAdmissionCall(job ImageJob) CallContext {
	startedAt := time.Time{}
	if job.AdmittedAt != nil {
		startedAt = *job.AdmittedAt
	}
	return CallContext{
		RequestID:             job.RequestID,
		Key:                   APIKey{ID: job.APIKeyID},
		StartedAt:             startedAt,
		TokenLimitBucket:      job.TokenLimitBucket,
		MinuteRequestHeld:     job.MinuteRequestHeld,
		ReservedTokens:        job.ReservedTokens,
		UserQuotaID:           userQuotaBucketKey(job.AttributedUserID),
		UserQuotaEnabled:      job.UserQuotaEnabled,
		UserMinuteRequestHeld: job.UserMinuteRequestHeld,
		UserTokenLimitBucket:  job.UserTokenLimitBucket,
		AttributedUserID:      job.AttributedUserID,
		RedisBillingAdmitted:  job.RedisBillingAdmitted,
		RedisKeyLeaseHeld:     job.RedisKeyLeaseHeld,
		RedisUserLeaseHeld:    job.RedisUserLeaseHeld,
	}
}
