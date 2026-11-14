package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestCodexGatewayEffortKeepsUltraAndConfigDefault(t *testing.T) {
	home, read := codexHome(t, "", "model_reasoning_effort = \"ultra\"\n")
	cx := codex(home)
	f := cx.Field("gateway_effort")
	if f == nil || !f.Live || !f.Quiet {
		t.Fatal("Codex has no live gateway effort control")
	}
	if err := f.Set("ultra"); err != nil {
		t.Fatal(err)
	}
	if f.Get() != "ultra" || provider.AgentEffort("codex") != "ultra" {
		t.Fatal("the live Ultra selection was not kept as Ultra")
	}
	if !strings.Contains(read(), `model_reasoning_effort = "ultra"`) {
		t.Fatal("the live override rewrote Codex's configured Ultra selection")
	}
	if err := f.Set(""); err != nil {
		t.Fatal(err)
	}
	if provider.AgentEffort("codex") != "" || !strings.Contains(read(), `model_reasoning_effort = "ultra"`) {
		t.Fatal("clearing a live override changed Codex's own effort default")
	}
}

// Profile-only wiring has its own Agent constructor. A live gateway
// pick is available there too, without creating the profile or changing
// the config.toml that plain codex reads.
func TestCodexProfileGatewayEffortLeavesFilesAlone(t *testing.T) {
	const config = "model_reasoning_effort = \"ultra\"\n"
	home, read := codexHome(t, "", config)
	if err := codex(home).Field("scope").Set("profile"); err != nil {
		t.Fatal(err)
	}
	cx := codex(home)
	f := cx.Field("gateway_effort")
	if f == nil || !f.Live || !f.Quiet {
		t.Fatal("profile-only Codex has no live gateway effort control")
	}
	for _, level := range []string{"ultra", "high", ""} {
		if err := cx.Apply("gateway_effort", level); err != nil {
			t.Fatal(err)
		}
		if f.Get() != level || provider.AgentEffort("codex") != level {
			t.Fatalf("profile gateway effort = %q, want %q", f.Get(), level)
		}
		if read() != config {
			t.Fatalf("a live pick changed config.toml: %s", read())
		}
		if _, err := os.Stat(cx.Path); !os.IsNotExist(err) {
			t.Fatalf("a live pick created or changed the profile: %v", err)
		}
	}
}
