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
	return a.RunClusterOperation(ctx, "subscription-account:"+id, operation)
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
