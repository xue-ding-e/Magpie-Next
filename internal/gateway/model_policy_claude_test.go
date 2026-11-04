package gateway

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/proc"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// A restrictive policy refuses Claude before even finding its binary.
// Haiku is resolved by Claude Code, so allowing that alias alone cannot
// establish which exact model the warm-up would spend.
func TestModelPolicyClaudeWarmupBeforeCLI(t *testing.T) {
	for _, allowed := range []string{"deepseek-v4.1-flash-free", "haiku"} {
		t.Run(allowed, func(t *testing.T) {
			fresh(t)
			allowOnly(t, allowed)
			t.Setenv("PATH", t.TempDir())
			if err := warmClaude(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "allowedModels") {
				t.Fatalf("warm-up reached CLI discovery instead of a local policy refusal: %v", err)
			}
		})
	}
}

func TestModelPolicyClaudeExactAndUnsetUseFakeCLI(t *testing.T) {
	fresh(t)
	log := fakeWarmClaude(t, `{"type":"result","is_error":false,"result":"Hi!"}`, 0)
	allowOnly(t, "claude-haiku-4-5")
	if err := warmClaude(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "allowedModels") {
		t.Fatalf("an unresolved alias passed an exact-model policy: %v", err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("forbidden warm-up started the fake CLI: %v", err)
	}
	if err := askClaude(t.Context(), "", "claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), "[--model][claude-haiku-4-5]") {
		t.Fatalf("allowed model did not run through the fake CLI: %v %s", err, b)
	}
	s := settings.Load()
	s.AllowedModels = nil
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if err := warmClaude(t.Context(), ""); err != nil {
		t.Fatalf("unset policy changed the existing warm-up: %v", err)
	}
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), "[--model][haiku]") {
		t.Fatalf("unset policy lost the default alias: %v %s", err, b)
	}
}

func TestModelPolicyClaudeDispatchRechecks(t *testing.T) {
	fresh(t)
	freshRoom(t, 1)
	allowOnly(t, "another-model")
	// This is the boundary after the CLI was found and a run slot was
	// acquired. Even a prepared command must be refused locally here.
	cmd := proc.CommandContext(t.Context(), "claude", "--version")
	err := runClaudeTree(t.Context(), cmd, "a policy test", "claude-haiku-4-5")
	if !errors.Is(err, provider.ErrModelPolicy) {
		t.Fatalf("prepared command reached process startup: %v", err)
	}
	claudeRoom.mu.Lock()
	live := claudeRoom.live
	claudeRoom.mu.Unlock()
	if live != 0 {
		t.Fatalf("local refusal held %d Claude run slots", live)
	}
}
