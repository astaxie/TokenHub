package server

import (
	"context"
	"errors"
	"io"
)

// The lease spans credential loading, model discovery, inference, and durable
// refresh acknowledgement. This prevents two replicas rotating one account's
// refresh token concurrently; it does not hold a database transaction open.
func (a *SubscriptionWorkerAdapter) withAccountLease(ctx context.Context, provider Provider, resource *ProviderResource, operation func(context.Context) error) error {
	var err error
	ctx, err = withSubscriptionWorkerOptions(ctx, a.Definition.ID, provider, resource)
	if err != nil {
		return err
	}
	id := provider.Options["resource_id"]
	if resource != nil {
		id = resource.ID
	}
	if id == "" || a.RunClusterOperation == nil {
		return operation(ctx)
	}
	// Cancellation stops acquisition and the operation, but the acquired lease
	// keeps renewing while account release drains and saves received rotations.
	leaseCtx, cancelLease := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelLease()
	stopWaiting := context.AfterFunc(ctx, cancelLease)
	defer stopWaiting()
	err = a.RunClusterOperation(leaseCtx, "subscription-account:"+id, func(heldCtx context.Context) error {
		if !stopWaiting() && ctx.Err() != nil {
			return ctx.Err()
		}
		operationBase := heldCtx
		deadline, hasDeadline := ctx.Deadline()
		if hasDeadline {
			var cancelDeadline context.CancelFunc
			operationBase, cancelDeadline = context.WithDeadline(heldCtx, deadline)
			defer cancelDeadline()
		}
		operationCtx, cancelOperation := context.WithCancelCause(operationBase)
		defer cancelOperation(nil)
		stopOperation := context.AfterFunc(ctx, func() {
			// Let the deadline timer preserve DeadlineExceeded in Err(), rather
			// than turning a timeout into explicit caller cancellation.
			if !hasDeadline || ctx.Err() != context.DeadlineExceeded {
				cancelOperation(context.Cause(ctx))
			}
		})
		defer stopOperation()
		return operation(operationCtx)
	})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (a *SubscriptionWorkerAdapter) Chat(ctx context.Context, provider Provider, model string, request ChatCompletionRequest) (any, Usage, error) {
	var result any
	var usage Usage
	err := a.withAccountLease(ctx, provider, nil, func(leaseCtx context.Context) error {
		var err error
		result, usage, err = a.chat(leaseCtx, provider, model, request)
		return err
	})
	var failure *subscriptionJSONFailure
	if errors.As(err, &failure) {
		usage = failure.usage
	}
	return result, usage, err
}

func (a *SubscriptionWorkerAdapter) ChatStream(ctx context.Context, provider Provider, model string, request ChatCompletionRequest, writer io.Writer) (Usage, error) {
	var usage Usage
	err := a.withAccountLease(ctx, provider, nil, func(leaseCtx context.Context) error {
		var err error
		usage, err = a.chatStream(leaseCtx, provider, model, request, writer)
		return err
	})
	var failure *subscriptionJSONFailure
	if errors.As(err, &failure) {
		usage = failure.usage
	}
	return usage, err
}

func (a *SubscriptionWorkerAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	var catalog ProviderCatalogEntry
	var status int
	err := a.withAccountLease(ctx, provider, &resource, func(leaseCtx context.Context) error {
		var err error
		catalog, status, err = a.resourceModels(leaseCtx, provider, resource, etag)
		return err
	})
	return catalog, status, err
}
