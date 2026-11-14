package provider

import (
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestCodexAgentEffortKeepsUltra(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SetAgentEffort(" Codex ", " ULTRA "); err != nil {
		t.Fatal(err)
	}
	if got := AgentEffort("codex"); got != "ultra" {
		t.Fatalf("Codex pick = %q, want ultra", got)
	}
	if got := settings.Load().AgentEfforts["codex"]; got != "ultra" {
		t.Fatalf("persisted Codex pick = %q, want ultra", got)
	}
	if err := SetAgentEffort("cursor-local", "ultra"); err == nil {
		t.Fatal("another agent accepted Codex's Ultra mode")
	}
	if _, effort := MemberEffort("p/m:ultra"); effort != "" {
		t.Fatal("Codex's Ultra changed group-member effort syntax")
	}
	if err := SetAgentEffort("codex", ""); err != nil {
		t.Fatal(err)
	}
	if got := AgentEffort("codex"); got != "" {
		t.Fatalf("cleared Codex pick = %q", got)
	}
}
