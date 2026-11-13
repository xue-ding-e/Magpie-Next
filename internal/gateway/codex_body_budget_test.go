package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexOversizedTurnCompactsBeforeUpstream(t *testing.T) {
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) > codexResponsesBodyBudget {
			t.Fatalf("summary request still exceeds budget: %d", len(body))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"bounded summary"}]}}`,
			`data: {"type":"response.completed","response":{"id":"resp_summary","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"bounded summary"}]}]}}`,
		))
	})
	large := strings.Repeat("tool output and user context ", 50_000)
	body := `{"model":"gpt-5.5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` + large + `"}]}]}`
	code, response := codexPost(t, body)
	if code != http.StatusOK || !strings.Contains(response, `"type":"compaction"`) {
		t.Fatalf("oversized turn was not compacted: %d %s", code, response)
	}
}

func TestCodexBodyBudgetScopeAndIdempotence(t *testing.T) {
	small := []byte(`{"model":"gpt-5.5","input":"small"}`)
	if codexBodyNeedsCompaction(small) {
		t.Fatal("small request crossed the byte budget")
	}
	large := []byte(`{"model":"gpt-5.5","input":"` + strings.Repeat("x", codexResponsesBodyBudget+100) + `"}`)
	if !codexBodyNeedsCompaction(large) {
		t.Fatal("large request did not cross the budget")
	}
	compact := codexBudgetedCompactBody(large)
	if len(compact) > codexResponsesBodyBudget || !json.Valid(compact) {
		t.Fatalf("bounded compaction body = %d bytes", len(compact))
	}
	if !isCodexOwnResponsesTurn("/responses", "gpt-5.5", large) || isCodexOwnResponsesTurn("/responses", "relay/gpt-5.5", large) || isCodexOwnResponsesTurn("/responses", "gpt-5.5", []byte(`{"input":[{"type":"compaction_trigger"}]}`)) {
		t.Fatal("budget scope is too broad")
	}
}
