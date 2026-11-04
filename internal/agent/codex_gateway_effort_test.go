package agent

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestCodexGatewayEffortKeepsUltraAndConfigDefault(t *testing.T) {
	home, read := codexHome(t, "", "model_reasoning_effort = \"ultra\"\n")
	cx := codex(home)
	f := cx.Field("gateway_effort")
	if f == nil || !f.Live {
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
