package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/codexsummary"
)

// The native endpoint and the account pool must both answer an ordinary
// turn. This also replays a summary already saved by an older Magpie.
func TestCodexLongTurnContinues(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		pool        bool
	}{
		{"sign-in", "gpt-5.5", false},
		{"through-pool", "gpt-5.5", true},
		{"named-pool", "codex/gpt-5.5", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const latest = "Please continue with the current task."
			const answer = "Continuing the task."
			history := strings.Repeat("Earlier result. ", 56_000)
			var mu sync.Mutex
			var sent []byte
			chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				sent = b
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				item := `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + answer + `"}]}`
				io.WriteString(w, sse(
					`data: {"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
					`data: {"type":"response.completed","response":{"id":"resp_long","status":"completed","output":[`+item+`]}}`,
				))
			})
			if tc.pool {
				codexSignedIn(t, "spare@example.com")
			}
			checkpoint := codexsummary.Encrypted("Keep the existing handoff.")
			body := `{"model":"` + tc.model + `","stream":true,"input":[` +
				`{"type":"compaction","encrypted_content":"` + checkpoint + `"},` +
				`{"type":"message","role":"user","content":[{"type":"input_text","text":"` + history + `"}]},` +
				`{"type":"message","role":"user","internal_chat_message_metadata_passthrough":{"turn_id":"desktop-only"},"content":[{"type":"input_text","text":"` + latest + `"}]}]}`
			code, reply := codexPost(t, body)
			if code != http.StatusOK || !strings.Contains(reply, answer) || strings.Contains(reply, `"type":"compaction"`) {
				t.Fatalf("ordinary long turn did not get its assistant answer: %d %.400s", code, reply)
			}
			mu.Lock()
			defer mu.Unlock()
			if got := gjson.GetBytes(sent, "input.2.content.0.text").String(); got != latest {
				t.Fatalf("last user message = %q, want %q", got, latest)
			}
			if got := gjson.GetBytes(sent, "input.1.content.0.text").String(); got != history {
				t.Fatalf("earlier history: got %d bytes, want %d", len(got), len(history))
			}
			if !strings.Contains(gjson.GetBytes(sent, "input.0.content.0.text").String(), "Keep the existing handoff.") {
				t.Fatal("the existing checkpoint was not restored")
			}
			if gjson.GetBytes(sent, "input.2.internal_chat_message_metadata_passthrough").Exists() {
				t.Fatal("desktop bookkeeping reached ChatGPT")
			}
		})
	}
}

func TestCodexBareByteCapSignalsClient(t *testing.T) {
	for _, tc := range []struct {
		name, model, refusal string
		size, wantStatus     int
	}{
		{"native", "gpt-5.5", `{"detail":"Bad Request"}`, 930_000, 200},
		{"pool", "codex/gpt-5.5", `{"detail":"Bad Request"}`, 930_000, 200},
		{"small", "gpt-5.5", `{"detail":"Bad Request"}`, 10_000, 400},
		{"named error", "gpt-5.5", `{"error":{"code":"invalid_encrypted_content","message":"Unrecognised encrypted content"}}`, 930_000, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, tc.refusal)
			})
			if strings.Contains(tc.model, "/") {
				codexSignedIn(t, "spare@example.com")
			}
			body := `{"model":"` + tc.model + `","stream":true,"input":[{"type":"message","role":"user","content":"` + strings.Repeat("x", tc.size) + `"}]}`
			req := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer chatgpt-token")
			req.Header.Set("User-Agent", codexUA)
			rec := httptest.NewRecorder()
			New().Handler().ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %.400s", rec.Code, tc.wantStatus, rec.Body)
			}
			if rec.Code == 200 && failedCode(rec.Body.String()) != "context_length_exceeded" {
				t.Fatalf("Codex did not receive a context overflow event: %.400s", rec.Body)
			}
		})
	}
}

// A client-requested compaction may initially carry the same oversized
// history as the refused turn. The smaller summary must be replayable.
func TestCodexBareByteCapCanCompactAndResume(t *testing.T) {
	const latest = "Continue the existing task"
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if len(b) >= 900_000 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"detail":"Bad Request"}`)
			return
		}
		text := "Resumed successfully."
		if strings.Contains(string(b), codexCompactPrompt[:45]) {
			if !strings.Contains(string(b), latest) {
				t.Error("bounded compaction lost the current task")
			}
			text = "Continue from the verified work."
		} else if !strings.Contains(string(b), "Continue from the verified work.") {
			t.Error("continuation did not replay the new summary")
		}
		item := `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}`
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(
			`data: {"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
			`data: {"type":"response.completed","response":{"id":"resp_recovered","status":"completed","output":[`+item+`]}}`,
		))
	})
	codexSignedIn(t, "spare@example.com")
	const base = `{"model":"codex/gpt-5.5","stream":true,"input":[`
	input := `{"type":"message","role":"user","content":"` + strings.Repeat("older history ", 70_000) + `"},` +
		`{"type":"message","role":"user","content":"` + latest + `"}`
	code, reply := codexPost(t, base+input+`,{"type":"compaction_trigger"}]}`)
	if code != 200 || compactedText(t, reply) != "Continue from the verified work." {
		t.Fatalf("client-requested compaction did not produce a summary: %d %.400s", code, reply)
	}
	seal := codexsummary.Encrypted(compactedText(t, reply))
	code, reply = codexPost(t, base+`{"type":"compaction","encrypted_content":"`+seal+`"},{"type":"message","role":"user","content":"go on"}]}`)
	if code != 200 || !strings.Contains(reply, "Resumed successfully.") {
		t.Fatalf("the compacted conversation could not continue: %d %.400s", code, reply)
	}
}

func TestCodexNativeByteCapRetriesExplicitCompaction(t *testing.T) {
	for _, path := range []string{"/responses", "/responses/compact", "pool"} {
		t.Run(path, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls++
				mu.Unlock()
				if len(body) >= 900_000 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					io.WriteString(w, `{"detail":"Bad Request"}`)
					return
				}
				if !strings.Contains(string(body), "Finish the current task.") {
					t.Error("native retry lost the current task")
				}
				if path == "/responses/compact" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"id":"resp_c","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"gAAAAAnative"}]}`)
					return
				}
				if !compactionTriggerIn(body) {
					t.Error("native retry lost the client's trigger")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, nativeSealedReply("gAAAAAnative"))
			})
			route, model := path, "gpt-5.5"
			if path == "pool" {
				codexSignedIn(t, "spare@example.com")
				nativeOn(t, "codex")
				route, model = "/responses", "codex/gpt-5.5"
			}
			input := `{"type":"message","role":"user","content":"` + strings.Repeat("earlier context ", 60_000) + `"},` +
				`{"type":"message","role":"user","content":"Finish the current task."}`
			if path != "/responses/compact" {
				input += `,{"type":"compaction_trigger"}`
			}
			body := `{"model":"` + model + `","stream":true,"input":[` + input + `]}`
			req := httptest.NewRequest("POST", CodexPath+route, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer chatgpt-token")
			req.Header.Set("User-Agent", codexUA)
			rec := httptest.NewRecorder()
			New().Handler().ServeHTTP(rec, req)
			mu.Lock()
			defer mu.Unlock()
			if rec.Code != 200 || calls != 2 || !strings.Contains(rec.Body.String(), "gAAAAAnative") {
				t.Fatalf("native compaction: %d, %d requests, %.400s", rec.Code, calls, rec.Body)
			}
		})
	}
}

func TestCodexSocketByteCapSignalsClient(t *testing.T) {
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"detail":"Bad Request"}`)
	})
	_, ts := sockServer(t)
	c, _, err := dialSock(t, ts, CodexPath+"/responses", http.Header{
		"Authorization": {"Bearer chatgpt-token"},
		"User-Agent":    {codexUA},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := `{"type":"response.create","model":"gpt-5.5","stream":true,"input":[{"type":"message","role":"user","content":"` +
		strings.Repeat("history ", 120_000) + `"}]}`
	end := last(c.turn(msg))
	if end.Get("type").Str != "response.failed" || end.Get("response.error.code").Str != "context_length_exceeded" {
		t.Fatalf("Codex's WebSocket did not get a context overflow event: %.400s", end.Raw)
	}
}
