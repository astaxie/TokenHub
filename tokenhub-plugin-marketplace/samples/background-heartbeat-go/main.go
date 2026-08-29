package main

import (
	"context"
	"fmt"
	"os"

	"tokenhub-plugin-marketplace/sdk/go/tokenhubplugin"
)

type heartbeatPayload struct {
	ResourceID string `json:"resource_id"`
	Count      int64  `json:"count"`
}

func main() {
	os.Exit(tokenhubplugin.ServeBackgroundJob(context.Background(), os.Stdin, os.Stdout, os.Stderr, handleBackgroundJob))
}

func handleBackgroundJob(ctx context.Context, invocation tokenhubplugin.BackgroundJobInvocation) (tokenhubplugin.BackgroundJobResult, error) {
	if invocation.PluginID != "tokenhub.background.heartbeat-go" || invocation.JobID != "heartbeat.ping" {
		return tokenhubplugin.BackgroundJobResult{}, fmt.Errorf("background job %s/%s is not supported", invocation.PluginID, invocation.JobID)
	}
	payload, err := tokenhubplugin.DecodeBackgroundPayload[heartbeatPayload](invocation)
	if err != nil {
		return tokenhubplugin.BackgroundJobResult{}, err
	}
	if payload.ResourceID == "" {
		return tokenhubplugin.BackgroundJobResult{}, fmt.Errorf("resource_id is required")
	}
	return tokenhubplugin.BackgroundJobResult{
		Data: map[string]any{
			"resource_id": payload.ResourceID,
			"heartbeat":   "ok",
			"trigger":     invocation.Trigger,
			"actor_id":    invocation.Actor.ID,
			"count":       payload.Count,
		},
		Metadata: map[string]string{"status": "ok"},
	}, nil
}
