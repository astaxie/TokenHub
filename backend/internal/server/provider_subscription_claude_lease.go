package server

import (
	"context"
	"errors"
)

// The renewable cluster lease remains held while a CLI process is parked on
// caller tools. Another replica must not rotate this account's refresh token
// until the process has stopped and its updated credentials are persisted.
func (r *claudeSubscriptionRun) launchWithLease(requestCtx context.Context, provider Provider, executable, model, instructions string, prompt []map[string]any, credentials ProviderResourceCredentials, request ChatCompletionRequest) error {
	ready := make(chan error, 1)
	runCtx := r.ctx
	leaseCtx, cancelLease := context.WithCancel(context.WithoutCancel(runCtx))
	stopWaiting := context.AfterFunc(runCtx, cancelLease)
	operation := func(heldCtx context.Context) error {
		if !stopWaiting() && runCtx.Err() != nil {
			return runCtx.Err()
		}
		// Stop the CLI when its lifetime ends or coordination is lost, while
		// renewing the account lease until credential cleanup has finished.
		operationCtx, cancelOperation := context.WithCancelCause(runCtx)
		defer cancelOperation(nil)
		stopHeld := context.AfterFunc(heldCtx, func() { cancelOperation(context.Cause(heldCtx)) })
		defer stopHeld()
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return context.Canceled
		}
		r.ctx = operationCtx
		// Credentials may have changed while another replica held the lease.
		fresh, err := subscriptionCredentials(operationCtx, provider, r.adapter.RefreshCredentials, false)
		if err == nil {
			credentials = fresh
			r.originalSession = fresh.SessionJSON
			r.accessToken = fresh.AccessToken
			r.credentialSnapshot = fresh.resourceSnapshot
			r.scope = claudeSubscriptionScope(r.resourceID, r.caller, r.accessToken, r.originalSession, r.model, r.credentialSnapshot)
			if fresh.SessionJSON != "" {
				err = validateClaudeSubscriptionSession(fresh.SessionJSON)
			} else if fresh.AccessToken == "" {
				err = NewHTTPError(400, "claude_subscription_credentials_required", "Import a Claude Code subscription token or account session")
			}
		}
		if err == nil {
			err = r.launch(executable, r.adapter.ExecutableArgs, model, instructions, prompt, credentials, request)
		}
		r.mu.Unlock()
		ready <- err
		if err != nil {
			r.close()
			<-r.cleanupDone
			return err
		}
		<-operationCtx.Done()
		r.close()
		<-r.cleanupDone
		return nil
	}
	go func() {
		defer cancelLease()
		defer stopWaiting()
		var err error
		if r.adapter.RunClusterOperation == nil {
			err = operation(leaseCtx)
		} else {
			err = r.adapter.RunClusterOperation(leaseCtx, "subscription-account:"+r.resourceID, operation)
		}
		select {
		case ready <- err:
		default:
		}
	}()
	select {
	case err := <-ready:
		if errors.Is(err, context.Canceled) && requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return err
	case <-requestCtx.Done():
		r.close()
		return requestCtx.Err()
	case <-runCtx.Done():
		r.close()
		if requestCtx.Err() != nil {
			return requestCtx.Err()
		}
		return runCtx.Err()
	}
}
