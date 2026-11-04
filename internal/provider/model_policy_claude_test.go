package provider

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestModelPolicyClaudeProbeBeforeCredentials(t *testing.T) {
	modelPolicyHome(t, []string{"deepseek-v4.1-flash-free"})
	var tokens, calls atomic.Int32
	old := claudeCLIProbe
	t.Cleanup(func() { claudeCLIProbe = old })
	claudeCLIProbe = func(context.Context, string, string) error { calls.Add(1); return nil }
	var p = Provider{ID: "claude", Account: &Account{Agent: "claude", token: func(context.Context) (string, error) {
		tokens.Add(1)
		return "fixture-config", nil
	}}}
	result := p.TestModels(t.Context(), []string{"claude-haiku-4-5"})[0]
	if result.OK || !strings.Contains(result.Error, "allowedModels") || tokens.Load() != 0 || calls.Load() != 0 {
		t.Fatalf("forbidden probe: OK=%t error=%q credential reads=%d CLI calls=%d", result.OK, result.Error, tokens.Load(), calls.Load())
	}
	s := settings.Load()
	s.AllowedModels = []string{"claude-haiku-4-5"}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if result := p.TestModels(t.Context(), []string{"claude-haiku-4-5"})[0]; !result.OK || calls.Load() != 1 {
		t.Fatalf("allowed probe: %+v, CLI calls=%d", result, calls.Load())
	}
}

func TestModelPolicyClaudeProbeRechecksAfterCredentials(t *testing.T) {
	modelPolicyHome(t, []string{"claude-haiku-4-5"})
	var calls atomic.Int32
	old := claudeCLIProbe
	t.Cleanup(func() { claudeCLIProbe = old })
	claudeCLIProbe = func(context.Context, string, string) error { calls.Add(1); return nil }
	var p = Provider{ID: "claude", Account: &Account{Agent: "claude", token: func(context.Context) (string, error) {
		s := settings.Load()
		s.AllowedModels = []string{"another-model"}
		return "fixture-config", settings.Save(s)
	}}}
	result := p.testClaude(t.Context(), "claude-haiku-4-5")
	if result.OK || !strings.Contains(result.Error, "allowedModels") || calls.Load() != 0 {
		t.Fatalf("stale probe: OK=%t error=%q CLI calls=%d", result.OK, result.Error, calls.Load())
	}
}
