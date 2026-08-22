package server

import (
	"context"
	"log"
)

func (s *GormStore) settleRedisBilling(kind string, call CallContext, actualTokens int64) {
	if s.billingRedis == nil || !call.RedisBillingAdmitted {
		return
	}
	if err := s.billingRedis.settle(context.Background(), call, actualTokens); err != nil {
		log.Printf("[tokenhub] failed to settle Redis billing %s request=%s: %v", kind, call.RequestID, err)
	}
}

func (s *GormStore) rollbackRedisBilling(kind string, call CallContext) {
	if s.billingRedis == nil || !call.RedisBillingAdmitted {
		return
	}
	if err := s.billingRedis.rollback(context.Background(), call); err != nil {
		log.Printf("[tokenhub] failed to rollback Redis billing %s request=%s: %v", kind, call.RequestID, err)
	}
}
