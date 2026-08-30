package plugin

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCommandSandboxPolicyUsesExplicitSafeEnvironment(t *testing.T) {
	policy, err := BuildCommandSandboxPolicy(CommandSandboxOptions{
		Dir:     t.TempDir(),
		Command: "command.sh",
		Timeout: time.Second,
		TempDir: "/tmp/tokenhub-plugin-test",
		Permissions: PermissionGrant{Enforced: true, Permissions: []PermissionDescriptor{
			{Kind: PermissionKindNetwork, Name: "api.example.com", Access: PermissionAccessConnect},
		}},
	})
	if err != nil {
		t.Fatalf("build sandbox policy: %v", err)
	}
	joined := strings.Join(policy.Env, "\n")
	for _, required := range []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PATH=", "TMPDIR=/tmp/tokenhub-plugin-test"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sandbox env = %v, want %s", policy.Env, required)
		}
	}
	for _, forbidden := range []string{"TOKENHUB_ADMIN_TOKEN=", "TOKENHUB_SECRET_KEY=", "OPENAI_API_KEY=", "DATABASE_URL="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("sandbox env leaked %s in %v", forbidden, policy.Env)
		}
	}
	if policy.NetworkEnforcement != SandboxEnforcementUnsupported || policy.ResourceEnforcement != SandboxEnforcementUnsupported {
		t.Fatalf("policy = %+v, want unsupported network/resource enforcement until OS sandbox exists", policy)
	}
	if policy.ProcessEnforcement != commandProcessEnforcementStatus() {
		t.Fatalf("process enforcement = %q, want platform status %q", policy.ProcessEnforcement, commandProcessEnforcementStatus())
	}
	if len(policy.AllowedNetwork) != 1 || policy.AllowedNetwork[0] != "api.example.com" {
		t.Fatalf("allowed network = %v, want api.example.com", policy.AllowedNetwork)
	}
}

func TestBuildCommandSandboxPolicyRejectsEscapingExecutable(t *testing.T) {
	_, err := BuildCommandSandboxPolicy(CommandSandboxOptions{
		Dir:     t.TempDir(),
		Command: "../command.sh",
	})
	if err == nil {
		t.Fatal("sandbox policy accepted escaping executable path")
	}
}
