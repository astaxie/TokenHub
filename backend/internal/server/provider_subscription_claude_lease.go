package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// The renewable cluster lease remains held while a CLI process is parked on
// caller tools. Another replica must not rotate this account's refresh token
// until the process has stopped and its updated credentials are persisted.
func (r *claudeSubscriptionRun) launchWithLease(requestCtx context.Context, provider Provider, executable, model, instructions string, prompt []map[string]any, credentials ProviderResourceCredentials, request ChatCompletionRequest) error {
	ready := make(chan error, 1)
	operation := func(leaseCtx context.Context) error {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return context.Canceled
		}
		r.ctx = leaseCtx
		// Credentials may have changed while another replica held the lease.
		fresh, err := subscriptionCredentials(leaseCtx, provider, r.adapter.RefreshCredentials, false)
		if err == nil {
			credentials = fresh
			r.originalSession = fresh.SessionJSON
			r.accessToken = fresh.AccessToken
			binding, _ := json.Marshal([]string{r.resourceID, r.caller, r.accessToken, r.originalSession, r.model})
			sum := sha256.Sum256(binding)
			r.scope = hex.EncodeToString(sum[:])
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
			return err
		}
		<-leaseCtx.Done()
		r.close()
		<-r.cleanupDone
		return nil
	}
	leaseCtx := r.ctx
	go func() {
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
		return err
	case <-requestCtx.Done():
		r.close()
		return requestCtx.Err()
	case <-leaseCtx.Done():
		r.close()
		return leaseCtx.Err()
	}
}
