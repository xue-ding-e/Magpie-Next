package cli

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
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
	// The qualified-model rule comes after the current upstream's suffix
	// split, so a level on a model cannot hand it to a role picker either.
	a.SplitSuffix = func(v string) (string, string, bool) {
		model, level, has := strings.Cut(v, ":")
		if has {
			return model, ":" + level, true
		}
		return v, "", true
	}
	for _, value := range []string{"relay/vendor/model:max", "group/smart:high"} {
		if f := fieldForValue(&a, value); f != nil {
			t.Errorf("%s selects %s after splitting its suffix, want the main model", value, f.Key)
		}
	}
}
