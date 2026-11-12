package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactionReplayPreservesNestedNumbers(t *testing.T) {
	for _, numbers := range []string{"9007199254740993,18446744073709551615,1.234567890123456789,-9007199254740993", "1e400,1e-400,-0,0.000000000000000000000001"} {
		item := `{"type":"function_call_output","call_id":"call_fixture","output":{"nested":[` + numbers + `]}}`
		input := []byte(`{"input":[{"type":"compaction","encrypted_content":"` + magpieCompaction + "c3ludGhldGlj" + `"},` + item + `]}`)
		got, _ := codexInputAs(input, false, false)
		var result struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Input) != 2 {
			t.Fatalf("input items changed: %s", got)
		}
		var tool struct {
			Type   string          `json:"type"`
			CallID string          `json:"call_id"`
			Output json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(result.Input[1], &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Type != "function_call_output" || tool.CallID != "call_fixture" || string(tool.Output) != `{"nested":[`+numbers+`]}` || !strings.Contains(string(result.Input[0]), "synthetic") {
			t.Fatalf("nested numbers or conversion changed: %s", got)
		}
	}
}
