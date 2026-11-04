package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
)

// A provider/model is still the main model when its picker omits it but
// another model field offers it, as Codex's subagent model picker can.
func TestQualifiedModelIsNotAnotherFieldsValue(t *testing.T) {
	options := func(values ...string) func(map[string]string) []agent.Option {
		return func(map[string]string) []agent.Option {
			var out []agent.Option
			for _, v := range values {
				out = append(out, agent.Option{Value: v})
			}
			return out
		}
	}
	var a = agent.Agent{Fields: []agent.Field{
		{Key: "model", Get: func() string { return "gpt-6.1-sol" }, Options: options("gpt-6.1-sol")},
		{Key: "effort", Get: func() string { return "high" }, Options: options("high", "medium")},
		{Key: "subagent_model", Get: func() string { return "" }, Options: options("codex/gpt-6.1-sol", "relay/vendor/model", "group/smart", "magpie/codex/gpt-6.1-sol", "gpt-6.1-sol")},
	}}
	for _, value := range []string{"codex/gpt-6.1-sol", "relay/vendor/model", "group/smart", "magpie/codex/gpt-6.1-sol", "gpt-6.1-sol"} {
		if f := fieldForValue(&a, value); f != nil {
			t.Errorf("%s selects %s, want the main model", value, f.Key)
		}
	}
	if f := fieldForValue(&a, "medium"); f == nil || f.Key != "effort" {
		t.Fatalf("medium selects %v, want effort", f)
	}
}

func TestCodexModelAndExplicitSubagentPicks(t *testing.T) {
	cliHome(t)
	path := writeHomeFile(t, filepath.Join(".codex", "config.toml"), "model = \"gpt-6.1-sol\"\nmodel_reasoning_effort = \"high\"\n")
	for _, c := range []struct {
		args  []string
		field string
		value string
	}{
		{[]string{"codex", "a/m"}, "model", "a/m"},
		{[]string{"codex", "medium"}, "effort", "medium"},
		{[]string{"codex", "subagent_model", "b/gpt-5.5"}, "subagent model", "b/gpt-5.5"},
	} {
		out, err := printed(t, func() error { return run(c.args) })
		if err != nil || !strings.HasPrefix(out, "✓ Codex "+c.field+" "+c.value+"\n") {
			t.Fatalf("magpie %s: %v %q", strings.Join(c.args, " "), err, out)
		}
		b, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(b), `model = "a/m"`) {
			t.Fatalf("main model changed after magpie %s: %v\n%s", strings.Join(c.args, " "), err, b)
		}
		wantSubagent := ""
		if c.field == "subagent model" {
			wantSubagent = c.value
		}
		if got := provider.CodexSubagentModel(); got != wantSubagent {
			t.Fatalf("magpie %s: subagent model = %q, want %q", strings.Join(c.args, " "), got, wantSubagent)
		}
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `model_reasoning_effort = "medium"`) {
		t.Fatalf("bare effort was not saved: %v\n%s", err, b)
	}
}

func TestCodexQualifiedModelOmittedFromMainPicker(t *testing.T) {
	cliHome(t)
	path := writeHomeFile(t, filepath.Join(".codex", "config.toml"), "model = \"gpt-6.1-sol\"\n")
	writeHomeFile(t, filepath.Join(".codex", "auth.json"), `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`)
	if err := provider.Save(provider.Provider{ID: "codex", Name: "Codex", Key: "test-key", Models: []string{"gpt-6.1-sol"}, Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	a, err := agent.Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range a.Fields[0].Options(a.Values()) {
		if o.Value == "codex/gpt-6.1-sol" {
			t.Fatal("fixture offers codex/gpt-6.1-sol in the main picker")
		}
	}
	found := false
	for _, o := range a.Field("subagent_model").Options(a.Values()) {
		found = found || o.Value == "codex/gpt-6.1-sol"
	}
	if !found {
		t.Fatal("fixture does not offer codex/gpt-6.1-sol for subagents")
	}
	out, err := printed(t, func() error { return run([]string{"codex", "codex/gpt-6.1-sol"}) })
	if err != nil || !strings.HasPrefix(out, "✓ Codex model codex/gpt-6.1-sol\n") {
		t.Fatalf("qualified model did not select the main model: %v %q", err, out)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `model = "codex/gpt-6.1-sol"`) || provider.CodexSubagentModel() != "" {
		t.Fatalf("qualified model changed the wrong setting: %v, subagent model %q\n%s", err, provider.CodexSubagentModel(), b)
	}
}
