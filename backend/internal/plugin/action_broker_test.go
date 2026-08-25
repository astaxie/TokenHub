package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestActionBrokerExecutesRegisteredHandler(t *testing.T) {
	broker := NewActionBroker()
	descriptor := ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Kind:     ActionKindRead,
	}
	if err := broker.Register(descriptor, ActionHandlerFunc(func(_ context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.Actor.ID != "admin_1" {
			t.Fatalf("actor id = %q, want admin_1", invocation.Actor.ID)
		}
		var payload map[string]string
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return ActionResult{Data: map[string]string{"resource_id": payload["resource_id"]}}, nil
	})); err != nil {
		t.Fatalf("register action: %v", err)
	}

	result, err := broker.Execute(context.Background(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Actor:    ActionActor{ID: "admin_1"},
		Payload:  json.RawMessage(`{"resource_id":"res_1"}`),
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	data := result.Data.(map[string]string)
	if data["resource_id"] != "res_1" {
		t.Fatalf("result data = %+v, want res_1", data)
	}
}

func TestActionBrokerReportsUnknownAction(t *testing.T) {
	_, err := NewActionBroker().Execute(context.Background(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "missing",
	})
	if !errors.Is(err, ErrPluginActionNotFound) {
		t.Fatalf("error = %v, want ErrPluginActionNotFound", err)
	}
}

func TestActionBrokerDescriptorOnlyActionIsUnavailable(t *testing.T) {
	broker := NewActionBroker()
	if err := broker.RegisterDescriptor(ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Kind:     ActionKindRead,
	}); err != nil {
		t.Fatalf("register descriptor: %v", err)
	}

	_, err := broker.Execute(context.Background(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
	})
	if !errors.Is(err, ErrPluginActionUnavailable) {
		t.Fatalf("error = %v, want ErrPluginActionUnavailable", err)
	}
}

func TestActionBrokerListsActionsDeterministically(t *testing.T) {
	broker := NewActionBroker()
	handler := ActionHandlerFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{}, nil
	})
	for _, descriptor := range []ActionDescriptor{
		{PluginID: "tokenhub.b", ActionID: "z", Kind: ActionKindRead},
		{PluginID: "tokenhub.a", ActionID: "b", Kind: ActionKindRead},
		{PluginID: "tokenhub.a", ActionID: "a", Kind: ActionKindRead},
	} {
		if err := broker.Register(descriptor, handler); err != nil {
			t.Fatalf("register action: %v", err)
		}
	}

	actions := broker.List()
	got := []string{actions[0].PluginID + ":" + actions[0].ActionID, actions[1].PluginID + ":" + actions[1].ActionID, actions[2].PluginID + ":" + actions[2].ActionID}
	want := []string{"tokenhub.a:a", "tokenhub.a:b", "tokenhub.b:z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func TestActionBrokerBindsHandlerAfterDescriptor(t *testing.T) {
	broker := NewActionBroker()
	descriptor := ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Kind:     ActionKindRead,
	}
	if err := broker.RegisterDescriptor(descriptor); err != nil {
		t.Fatalf("register descriptor: %v", err)
	}
	if err := broker.Register(descriptor, ActionHandlerFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("bind handler: %v", err)
	}

	result, err := broker.Execute(context.Background(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	if result.Data != "ok" {
		t.Fatalf("result data = %v, want ok", result.Data)
	}
}

func TestActionBrokerValidatesInputSchemaBeforeHandler(t *testing.T) {
	broker := NewActionBroker()
	calls := 0
	descriptor := ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Kind:     ActionKindRead,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"refresh":     map[string]any{"type": "boolean"},
			},
		},
	}
	if err := broker.Register(descriptor, ActionHandlerFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		calls++
		return ActionResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register action: %v", err)
	}

	for _, payload := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"resource_id":7}`),
		json.RawMessage(`{"resource_id":"res_1","refresh":"yes"}`),
		json.RawMessage(`not-json`),
	} {
		_, err := broker.Execute(context.Background(), ActionInvocation{
			PluginID: "tokenhub.test",
			ActionID: "quota.read",
			Payload:  payload,
		})
		if !errors.Is(err, ErrPluginActionInvalidPayload) {
			t.Fatalf("payload %s error = %v, want ErrPluginActionInvalidPayload", payload, err)
		}
	}
	if calls != 0 {
		t.Fatalf("handler was called %d times for invalid payloads", calls)
	}

	result, err := broker.Execute(context.Background(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Payload:  json.RawMessage(`{"resource_id":"res_1","refresh":true}`),
	})
	if err != nil {
		t.Fatalf("execute valid action: %v", err)
	}
	if result.Data != "ok" || calls != 1 {
		t.Fatalf("valid action result=%+v calls=%d, want ok and one call", result, calls)
	}
}

func TestActionBrokerRejectsInvalidDescriptor(t *testing.T) {
	err := NewActionBroker().Register(ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "bad",
		Kind:     ActionKind("danger"),
	}, ActionHandlerFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{}, nil
	}))
	if err == nil {
		t.Fatal("invalid action kind was accepted")
	}
}

func TestActionBrokerRejectsDuplicateAction(t *testing.T) {
	broker := NewActionBroker()
	handler := ActionHandlerFunc(func(context.Context, ActionInvocation) (ActionResult, error) {
		return ActionResult{}, nil
	})
	descriptor := ActionDescriptor{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Kind:     ActionKindRead,
	}
	if err := broker.Register(descriptor, handler); err != nil {
		t.Fatalf("register action: %v", err)
	}
	if err := broker.Register(descriptor, handler); err == nil {
		t.Fatal("duplicate action was accepted")
	}
}
