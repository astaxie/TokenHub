package server

import (
	"context"
	"database/sql"
	"errors"
)

// Caller cancellation stops acquisition and upstream work. Once acquired, the
// refresh lease keeps renewing through bounded persistence of a received token.
func (s *GormStore) withNativeCredentialRefreshLease(ctx context.Context, resourceID string, operation func(context.Context, context.Context) error) error {
	leaseCtx, cancelLease := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelLease()
	stopWaiting := context.AfterFunc(ctx, cancelLease)
	defer stopWaiting()
	err := s.withClusterLease(leaseCtx, "credential-refresh:"+resourceID, func(heldCtx context.Context) error {
		if !stopWaiting() && ctx.Err() != nil {
			return ctx.Err()
		}
		operationCtx, cancelOperation := context.WithCancelCause(ctx)
		defer cancelOperation(nil)
		stopHeld := context.AfterFunc(heldCtx, func() { cancelOperation(context.Cause(heldCtx)) })
		defer stopHeld()
		return operation(operationCtx, heldCtx)
	})
	if ctx.Err() != nil && (err == nil || errors.Is(err, context.Canceled) || errors.Is(err, sql.ErrTxDone)) {
		return ctx.Err()
	}
	return err
}
