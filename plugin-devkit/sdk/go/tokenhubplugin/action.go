package tokenhubplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	ActionKindRead             = "read"
	ActionKindTest             = "test"
	ActionKindMutate           = "mutate"
	ActionKindExternalRedirect = "external_redirect"
	ActionKindImportExport     = "import_export"
)

type ActionActor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
}

type ActionInvocation struct {
	PluginID string          `json:"plugin_id"`
	ActionID string          `json:"action_id"`
	Actor    ActionActor     `json:"actor"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type ActionResult struct {
	Data        any               `json:"data,omitempty"`
	RedirectURL string            `json:"redirect_url,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type ActionHandler func(context.Context, ActionInvocation) (ActionResult, error)

func ServeAction(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, handler ActionHandler) int {
	if handler == nil {
		fmt.Fprintln(stderr, "action handler is required")
		return 2
	}
	var invocation ActionInvocation
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invocation); err != nil {
		fmt.Fprintf(stderr, "decode action invocation: %v\n", err)
		return 2
	}
	if strings.TrimSpace(invocation.PluginID) == "" || strings.TrimSpace(invocation.ActionID) == "" {
		fmt.Fprintln(stderr, "plugin_id and action_id are required")
		return 2
	}
	result, err := handler(ctx, invocation)
	if err != nil {
		fmt.Fprintf(stderr, "execute action %s/%s: %v\n", invocation.PluginID, invocation.ActionID, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode action result: %v\n", err)
		return 1
	}
	return 0
}

func DecodeActionPayload[T any](invocation ActionInvocation) (T, error) {
	var value T
	if len(invocation.Payload) == 0 {
		return value, nil
	}
	if err := json.Unmarshal(invocation.Payload, &value); err != nil {
		return value, fmt.Errorf("decode action payload: %w", err)
	}
	return value, nil
}
