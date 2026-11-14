package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/provider"
)

func TestCodexDesktopMetadataDoesNotReachBackend(t *testing.T) {
	for _, routed := range []bool{false, true} {
		name := "native"
		if routed {
			name = "routed"
		}
		t.Run(name, func(t *testing.T) {
			if routed {
				codexSignedIn(t, "spare@example.com")
			} else {
				fresh(t)
				setHome(t, t.TempDir())
			}
			var mu sync.Mutex
			var received [][]byte
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				received = append(received, bytes.Clone(body))
				mu.Unlock()
				for _, item := range gjson.GetBytes(body, "input").Array() {
					if item.Get("internal_chat_message_metadata_passthrough").Exists() {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(400)
						io.WriteString(w, `{"detail":"Bad Request"}`)
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, sse(`data: {"type":"response.output_text.delta","delta":"desktop metadata removed"}`, `data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[]}}`))
			}))
			t.Cleanup(up.Close)
			was := provider.CodexBase
			provider.CodexBase = up.URL + "/backend-api/codex"
			t.Cleanup(func() { provider.CodexBase = was })
			const body = `{"model":"gpt-5.5","stream":true,"input":[` +
				`{"type":"reasoning","summary":[],"encrypted_content":"synthetic-reasoning-seal","internal_chat_message_metadata_passthrough":{"turn_id":"synthetic-turn"}},` +
				`{"type":"custom_tool_call","name":"exec","call_id":"call_1","input":"original tool code","encrypted_content":"synthetic-tool-seal","internal_chat_message_metadata_passthrough":{"create_time":123456789012345678901234567890}},` +
				`{"type":"custom_tool_call_output","call_id":"call_1","output":"original tool result","internal_chat_message_metadata_passthrough":{"turn_id":"synthetic-turn"}},` +
				`{"type":"function_call_output","name":"send_message_to_thread","namespace":"codex_app","output":"original notification","internal_chat_message_metadata_passthrough":{"turn_id":"synthetic-turn"}}]}`
			code, response := codexPost(t, body)
			if code != 200 || !strings.Contains(response, "desktop metadata removed") {
				t.Fatalf("desktop history was refused: %d %s", code, response)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(received) != 1 {
				t.Fatalf("made %d requests; bookkeeping should be removed before the first request", len(received))
			}
			got := received[0]
			for path, want := range map[string]string{"input.0.encrypted_content": "synthetic-reasoning-seal", "input.1.encrypted_content": "synthetic-tool-seal", "input.1.call_id": "call_1", "input.1.input": "original tool code", "input.2.output": "original tool result", "input.3.type": "function_call_output", "input.3.output": "original notification"} {
				if gjson.GetBytes(got, path).String() != want {
					t.Fatalf("wire cleanup changed %s: %s", path, got)
				}
			}
			if gjson.GetBytes(got, "input.3.call_id").Exists() {
				t.Fatal("invented a call ID for the notification")
			}
		})
	}
}

func TestResponsesWireMetadataScopeAndIdentity(t *testing.T) {
	const body = `{"input":[{"type":"function_call","arguments":"{\"internal_chat_message_metadata_passthrough\":\"business value\"}","call_id":"call_1","counter":123456789012345678901234567890,"internal_chat_message_metadata_passthrough":{"turn_id":"client only"}},{"type":"message","content":[{"type":"input_text","text":"internal_chat_message_metadata_passthrough is literal user text"}]}]}`
	fixed := responsesWireBody([]byte(body))
	if !json.Valid(fixed) || gjson.GetBytes(fixed, "input.0.internal_chat_message_metadata_passthrough").Exists() {
		t.Fatal("client metadata remains on the wire")
	}
	if gjson.GetBytes(fixed, "input.0.arguments").String() != gjson.Get(body, "input.0.arguments").String() || gjson.GetBytes(fixed, "input.0.counter").Raw != "123456789012345678901234567890" || gjson.GetBytes(fixed, "input.1.content.0.text").String() != gjson.Get(body, "input.1.content.0.text").String() {
		t.Fatal("cleanup changed user content or numeric precision")
	}
	if !bytes.Equal(responsesWireBody(fixed), fixed) {
		t.Fatal("wire cleanup is not idempotent")
	}
	const untouched = `{ "input": [{"type":"message","content":"unchanged"}], "metadata":{"internal_chat_message_metadata_passthrough":"user value"} }`
	if !bytes.Equal(responsesWireBody([]byte(untouched)), []byte(untouched)) {
		t.Fatal("unrelated user data triggered a rewrite")
	}
}
