package server

import "context"

type mediaChatStreamLimitKey struct{}

// Some media providers send the entire generated audio in one Chat event. Use
// the admitted model's modality so client fields cannot widen a text stream.
func withMediaChatStreamLimit(ctx context.Context, model Model) context.Context {
	if modelHasMediaOutput(model) {
		return context.WithValue(ctx, mediaChatStreamLimitKey{}, maxMediaResponseBytes)
	}
	return ctx
}

func mediaChatStreamLimit(ctx context.Context) int {
	limit, _ := ctx.Value(mediaChatStreamLimitKey{}).(int)
	return limit
}
