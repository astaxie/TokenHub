package server

import (
	"context"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPluginBackgroundSchedulerStartsAndRestartsWithServerLifecycle(t *testing.T) {
	server := New(NewMemoryStore())

	server.StartBillingScheduler()
	state := server.pluginBackgroundRunner.SchedulerState()
	if state.Status != pluginmeta.BackgroundSchedulerRunning || state.Generation != 1 {
		t.Fatalf("started plugin background scheduler state = %+v, want running generation 1", state)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown server: %v", err)
	}
	state = server.pluginBackgroundRunner.SchedulerState()
	if state.Status != pluginmeta.BackgroundSchedulerStopped || state.Generation != 1 {
		t.Fatalf("stopped plugin background scheduler state = %+v, want stopped generation 1", state)
	}

	server.StartBillingScheduler()
	state = server.pluginBackgroundRunner.SchedulerState()
	if state.Status != pluginmeta.BackgroundSchedulerRunning || state.Generation != 2 {
		t.Fatalf("restarted plugin background scheduler state = %+v, want running generation 2", state)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown server: %v", err)
	}
}
