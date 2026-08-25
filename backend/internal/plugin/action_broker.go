package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrPluginActionNotFound = errors.New("plugin action is not registered")

type ActionKind string

const (
	ActionKindRead             ActionKind = "read"
	ActionKindTest             ActionKind = "test"
	ActionKindMutate           ActionKind = "mutate"
	ActionKindExternalRedirect ActionKind = "external_redirect"
	ActionKindImportExport     ActionKind = "import_export"
)

type ActionDescriptor struct {
	PluginID     string         `json:"plugin_id"`
	ActionID     string         `json:"action_id"`
	Kind         ActionKind     `json:"kind"`
	Title        string         `json:"title,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

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

type ActionHandler interface {
	ExecutePluginAction(ctx context.Context, invocation ActionInvocation) (ActionResult, error)
}

type ActionHandlerFunc func(ctx context.Context, invocation ActionInvocation) (ActionResult, error)

func (f ActionHandlerFunc) ExecutePluginAction(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	return f(ctx, invocation)
}

type ActionBroker struct {
	actions  map[string]ActionDescriptor
	handlers map[string]ActionHandler
}

func NewActionBroker() *ActionBroker {
	return &ActionBroker{
		actions:  map[string]ActionDescriptor{},
		handlers: map[string]ActionHandler{},
	}
}

func (b *ActionBroker) Register(descriptor ActionDescriptor, handler ActionHandler) error {
	if b == nil {
		return fmt.Errorf("plugin action broker is not configured")
	}
	descriptor = NormalizeActionDescriptor(descriptor)
	if descriptor.PluginID == "" {
		return fmt.Errorf("plugin action plugin id is required")
	}
	if descriptor.ActionID == "" {
		return fmt.Errorf("plugin action id is required")
	}
	if !validActionKind(descriptor.Kind) {
		return fmt.Errorf("unsupported plugin action kind %q", descriptor.Kind)
	}
	if handler == nil {
		return fmt.Errorf("plugin action handler is required")
	}
	key := pluginActionKey(descriptor.PluginID, descriptor.ActionID)
	if _, ok := b.actions[key]; ok {
		return fmt.Errorf("plugin action %s from plugin %s is already registered", descriptor.ActionID, descriptor.PluginID)
	}
	b.actions[key] = descriptor
	b.handlers[key] = handler
	return nil
}

func (b *ActionBroker) Execute(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	if b == nil {
		return ActionResult{}, fmt.Errorf("plugin action broker is not configured")
	}
	invocation.PluginID = strings.TrimSpace(invocation.PluginID)
	invocation.ActionID = strings.TrimSpace(invocation.ActionID)
	handler := b.handlers[pluginActionKey(invocation.PluginID, invocation.ActionID)]
	if handler == nil {
		return ActionResult{}, ErrPluginActionNotFound
	}
	return handler.ExecutePluginAction(ctx, invocation)
}

func (b *ActionBroker) Describe(pluginID string, actionID string) (ActionDescriptor, bool) {
	if b == nil {
		return ActionDescriptor{}, false
	}
	descriptor, ok := b.actions[pluginActionKey(pluginID, actionID)]
	return descriptor, ok
}

func (b *ActionBroker) List() []ActionDescriptor {
	if b == nil {
		return nil
	}
	items := make([]ActionDescriptor, 0, len(b.actions))
	for _, descriptor := range b.actions {
		items = append(items, descriptor)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PluginID != items[j].PluginID {
			return items[i].PluginID < items[j].PluginID
		}
		return items[i].ActionID < items[j].ActionID
	})
	return items
}

func NormalizeActionDescriptor(descriptor ActionDescriptor) ActionDescriptor {
	descriptor.PluginID = strings.TrimSpace(descriptor.PluginID)
	descriptor.ActionID = strings.TrimSpace(descriptor.ActionID)
	descriptor.Title = strings.TrimSpace(descriptor.Title)
	if descriptor.Kind == "" {
		descriptor.Kind = ActionKindRead
	}
	return descriptor
}

func validActionKind(kind ActionKind) bool {
	switch kind {
	case ActionKindRead, ActionKindTest, ActionKindMutate, ActionKindExternalRedirect, ActionKindImportExport:
		return true
	default:
		return false
	}
}

func pluginActionKey(pluginID string, actionID string) string {
	return strings.TrimSpace(pluginID) + "\x00" + strings.TrimSpace(actionID)
}
