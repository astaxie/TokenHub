package main

import (
	"context"
	"fmt"
	"os"

	"tokenhub-plugin-marketplace/sdk/go/tokenhubplugin"
)

type echoPayload struct {
	ResourceID string `json:"resource_id"`
	Message    string `json:"message"`
	DryRun     bool   `json:"dry_run"`
}

func main() {
	os.Exit(tokenhubplugin.ServeAction(context.Background(), os.Stdin, os.Stdout, os.Stderr, handleAction))
}

func handleAction(ctx context.Context, invocation tokenhubplugin.ActionInvocation) (tokenhubplugin.ActionResult, error) {
	if invocation.PluginID != "tokenhub.action.echo-go" || invocation.ActionID != "echo.run" {
		return tokenhubplugin.ActionResult{}, fmt.Errorf("action %s/%s is not supported", invocation.PluginID, invocation.ActionID)
	}
	payload, err := tokenhubplugin.DecodeActionPayload[echoPayload](invocation)
	if err != nil {
		return tokenhubplugin.ActionResult{}, err
	}
	if payload.ResourceID == "" {
		return tokenhubplugin.ActionResult{}, fmt.Errorf("resource_id is required")
	}
	message := payload.Message
	if message == "" {
		message = "pong"
	}
	return tokenhubplugin.ActionResult{
		Data: map[string]any{
			"resource_id": payload.ResourceID,
			"message":     message,
			"dry_run":     payload.DryRun,
			"actor_id":    invocation.Actor.ID,
		},
		Metadata: map[string]string{"status": "ok"},
	}, nil
}
