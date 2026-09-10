package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestBuiltinProviderHandlersFollowLifecycleAcrossRestart(t *testing.T) {
	config := Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()}
	server := NewWithConfig(NewMemoryStore(), config)
	pluginID := "tokenhub.provider.openai-codex"
	assertBuiltinCodexHandlersEnabled(t, server)

	patchBuiltinPresentationStatus(t, server, pluginID, pluginmeta.StatusDisabled)
	assertBuiltinCodexHandlersDisabled(t, server)
	restarted := NewWithConfig(NewMemoryStore(), config)
	assertBuiltinCodexHandlersDisabled(t, restarted)

	patchBuiltinPresentationStatus(t, restarted, pluginID, pluginmeta.StatusEnabled)
	assertBuiltinCodexHandlersEnabled(t, restarted)
}

func TestFailedBuiltinProviderStatesDoNotPublishHandlers(t *testing.T) {
	for _, status := range []pluginmeta.Status{pluginmeta.StatusFailedValidation, pluginmeta.StatusFailedStartup} {
		t.Run(string(status), func(t *testing.T) {
			server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
			patchBuiltinPresentationStatus(t, server, "tokenhub.provider.openai-codex", status)
			assertBuiltinCodexHandlersDisabled(t, server)
		})
	}
}

func TestDisablingOneBuiltinProviderPreservesOtherProviderActions(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	pluginID := "tokenhub.provider.kronk"
	actions := []pluginmeta.ActionDescriptor{}
	for _, action := range server.pluginActions.List() {
		if action.PluginID == pluginID {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		t.Fatal("enabled Kronk Provider has no actions")
	}
	patchBuiltinPresentationStatus(t, server, pluginID, pluginmeta.StatusDisabled)
	for _, action := range actions {
		if _, found := server.pluginActions.Describe(pluginID, action.ActionID); found {
			t.Fatalf("disabled Kronk Provider still publishes action %s", action.ActionID)
		}
	}
	assertBuiltinCodexHandlersEnabled(t, server)
}

func assertBuiltinCodexHandlersDisabled(t *testing.T, server *Server) {
	t.Helper()
	pluginID := "tokenhub.provider.openai-codex"
	actionID := "openai_codex.oauth.start"
	jobID := "openai_codex.credentials.refresh_due"
	if _, err := server.pluginActions.Execute(context.Background(), pluginmeta.ActionInvocation{PluginID: pluginID, ActionID: actionID}); !errors.Is(err, pluginmeta.ErrPluginActionNotFound) {
		t.Fatalf("disabled direct action error = %v; want action not found", err)
	}
	if _, err := server.pluginBackgroundRunner.Run(context.Background(), pluginmeta.BackgroundJobInvocation{PluginID: pluginID, JobID: jobID}); !errors.Is(err, pluginmeta.ErrPluginBackgroundJobNotFound) {
		t.Fatalf("disabled direct job error = %v; want job not found", err)
	}
	for _, endpoint := range []struct{ path, code string }{
		{"/api/admin/plugins/" + pluginID + "/actions/" + actionID, "plugin_action_not_found"},
		{"/api/admin/provider-actions/" + ProviderOpenAICodex + "/oauth.start", "provider_action_not_found"},
		{"/api/admin/plugins/" + pluginID + "/background-jobs/" + jobID + "/run", "plugin_background_job_not_found"},
	} {
		response := doJSON(t, server.Handler(), http.MethodPost, endpoint.path, map[string]any{}, "dev_admin_token")
		assertResponseBodyJSONError(t, response, http.StatusNotFound, endpoint.code)
	}
	for _, job := range server.pluginBackgroundJobs.List() {
		if job.PluginID == pluginID {
			t.Fatalf("disabled plugin still schedules job %s", job.JobID)
		}
	}
	for _, record := range server.pluginBackgroundRunner.RunDue(context.Background(), time.Now().UTC().Add(time.Hour), "schedule") {
		if record.PluginID == pluginID {
			t.Fatalf("disabled plugin job ran on schedule: %+v", record)
		}
	}
}

func assertBuiltinCodexHandlersEnabled(t *testing.T, server *Server) {
	t.Helper()
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.start", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("enabled OAuth action: %d %s", response.Code, response.Body)
	}
	records := server.pluginBackgroundRunner.RunDue(context.Background(), time.Now().UTC().Add(time.Hour), "schedule")
	count := 0
	for _, record := range records {
		if record.PluginID == "tokenhub.provider.openai-codex" {
			count++
			if record.Status != pluginmeta.BackgroundJobRunSucceeded {
				t.Fatalf("enabled scheduled job failed: %+v", record)
			}
		}
	}
	if count != 2 {
		t.Fatalf("enabled Codex plugin ran %d scheduled jobs; want 2", count)
	}
}
