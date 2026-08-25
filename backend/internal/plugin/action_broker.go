package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var (
	ErrPluginActionNotFound       = errors.New("plugin action is not registered")
	ErrPluginActionUnavailable    = errors.New("plugin action handler is unavailable")
	ErrPluginActionInvalidPayload = errors.New("plugin action payload is invalid")
	ErrPluginActionInvalidResult  = errors.New("plugin action result is invalid")
)

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
	Capability   string         `json:"capability,omitempty"`
	Subject      string         `json:"subject,omitempty"`
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

type unavailableActionHandler struct{}

func (unavailableActionHandler) ExecutePluginAction(context.Context, ActionInvocation) (ActionResult, error) {
	return ActionResult{}, ErrPluginActionUnavailable
}

type ActionBroker struct {
	actions map[string]actionEntry
}

type actionEntry struct {
	descriptor ActionDescriptor
	handler    ActionHandler
}

func NewActionBroker() *ActionBroker {
	return &ActionBroker{
		actions: map[string]actionEntry{},
	}
}

func (b *ActionBroker) RegisterDescriptor(descriptor ActionDescriptor) error {
	return b.register(descriptor, unavailableActionHandler{}, false)
}

func (b *ActionBroker) Register(descriptor ActionDescriptor, handler ActionHandler) error {
	return b.register(descriptor, handler, true)
}

func (b *ActionBroker) register(descriptor ActionDescriptor, handler ActionHandler, allowDescriptorBinding bool) error {
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
	if existing, ok := b.actions[key]; ok && !(allowDescriptorBinding && isUnavailableActionHandler(existing.handler)) {
		return fmt.Errorf("plugin action %s from plugin %s is already registered", descriptor.ActionID, descriptor.PluginID)
	}
	b.actions[key] = actionEntry{descriptor: descriptor, handler: handler}
	return nil
}

func (b *ActionBroker) Execute(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	if b == nil {
		return ActionResult{}, fmt.Errorf("plugin action broker is not configured")
	}
	invocation.PluginID = strings.TrimSpace(invocation.PluginID)
	invocation.ActionID = strings.TrimSpace(invocation.ActionID)
	entry, ok := b.actions[pluginActionKey(invocation.PluginID, invocation.ActionID)]
	if !ok {
		return ActionResult{}, ErrPluginActionNotFound
	}
	if err := validateActionInvocationPayload(entry.descriptor.InputSchema, invocation.Payload); err != nil {
		return ActionResult{}, err
	}
	result, err := entry.handler.ExecutePluginAction(ctx, invocation)
	if err != nil {
		return ActionResult{}, err
	}
	if err := validateActionResultData(entry.descriptor.OutputSchema, result.Data); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}

func (b *ActionBroker) Describe(pluginID string, actionID string) (ActionDescriptor, bool) {
	if b == nil {
		return ActionDescriptor{}, false
	}
	entry, ok := b.actions[pluginActionKey(pluginID, actionID)]
	return entry.descriptor, ok
}

func (b *ActionBroker) List() []ActionDescriptor {
	if b == nil {
		return nil
	}
	items := make([]ActionDescriptor, 0, len(b.actions))
	for _, entry := range b.actions {
		items = append(items, entry.descriptor)
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
	descriptor.Capability = strings.TrimSpace(descriptor.Capability)
	descriptor.Subject = strings.TrimSpace(descriptor.Subject)
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

func isUnavailableActionHandler(handler ActionHandler) bool {
	_, ok := handler.(unavailableActionHandler)
	return ok
}

func validateActionInvocationPayload(schema map[string]any, payload json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var value any
	if len(bytes.TrimSpace(payload)) == 0 {
		value = map[string]any{}
	} else if err := json.Unmarshal(payload, &value); err != nil {
		return fmt.Errorf("%w: JSON could not be decoded", ErrPluginActionInvalidPayload)
	}
	if err := validateActionSchemaValue("$", value, schema, ErrPluginActionInvalidPayload); err != nil {
		return err
	}
	return nil
}

func validateActionResultData(schema map[string]any, data any) error {
	if len(schema) == 0 {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w: JSON could not be encoded", ErrPluginActionInvalidResult)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: JSON could not be decoded", ErrPluginActionInvalidResult)
	}
	if err := validateActionSchemaValue("$.data", value, schema, ErrPluginActionInvalidResult); err != nil {
		return err
	}
	return nil
}

func validateActionSchemaValue(path string, value any, schema map[string]any, kind error) error {
	schemaType := schemaString(schema["type"])
	if schemaType != "" && !actionSchemaTypeMatches(value, schemaType) {
		return fmt.Errorf("%w: %s must be %s", kind, path, schemaType)
	}
	if schemaType != "" && schemaType != "object" {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		if schemaType == "object" {
			return fmt.Errorf("%w: %s must be object", kind, path)
		}
		return nil
	}
	for _, field := range schemaStringList(schema["required"]) {
		if _, ok := object[field]; !ok {
			return fmt.Errorf("%w: %s.%s is required", kind, path, field)
		}
	}
	for name, childSchema := range schemaObjectProperties(schema["properties"]) {
		child, ok := object[name]
		if !ok {
			continue
		}
		if err := validateActionSchemaValue(path+"."+name, child, childSchema, kind); err != nil {
			return err
		}
	}
	return nil
}

func actionSchemaTypeMatches(value any, schemaType string) bool {
	switch schemaType {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return true
	}
}

func schemaString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func schemaStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return normalizeStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := schemaString(item); text != "" {
				result = append(result, text)
			}
		}
		return normalizeStrings(result)
	default:
		return nil
	}
}

func schemaObjectProperties(value any) map[string]map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := map[string]map[string]any{}
	for name, child := range raw {
		childSchema, ok := child.(map[string]any)
		if !ok {
			continue
		}
		if field := strings.TrimSpace(name); field != "" {
			result[field] = childSchema
		}
	}
	return result
}
