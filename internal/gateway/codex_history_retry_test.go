package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/provider"
)

// A native turn must end with the answer to the user's current request.
// The real backend can refuse the replay before it starts a response.
func TestCodexOversizedTurnRecoversVisibleAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, path, endpoint, refusal string
		compressed, retries           bool
	}{
		{"native turn", CodexPath + "/responses", "/responses", `{"detail":"Bad Request"}`, true, true},
		{"native compaction", CodexPath + "/responses/compact", "/responses/compact", `{"detail":"Bad Request"}`, false, true},
		{"account pool turn", "/v1/responses", "/responses", `{"detail":"Bad Request"}`, true, true},
		{"specific bad request", CodexPath + "/responses", "/responses", `{"detail":"Invalid image"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zstdTurns(t)
			var mu sync.Mutex
			var sent [][]byte
			if tc.path == "/v1/responses" {
				codexSignedIn(t, "spare@example.com")
			} else {
				setHome(t, t.TempDir())
			}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != CodexPath+tc.endpoint {
					t.Errorf("sent to %s, want %s", r.URL.Path, CodexPath+tc.endpoint)
				}
				body, _ := io.ReadAll(r.Body)
				if (r.Header.Get("Content-Encoding") == "zstd") != tc.compressed {
					t.Error("backend received the wrong encoding")
				}
				if tc.compressed {
					dec, err := zstd.NewReader(nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer dec.Close()
					body, err = dec.DecodeAll(body, nil)
					if err != nil {
						t.Error(err)
						return
					}
				}
				mu.Lock()
				sent = append(sent, body)
				mu.Unlock()
				if len(body) > codexResponsesBodyBudget {
					w.WriteHeader(400)
					io.WriteString(w, tc.refusal)
					return
				}
				if tc.endpoint == "/responses/compact" {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"native-checkpoint"}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, sse(
					`data: {"type":"response.output_text.delta","delta":"answer to the current request"}`,
					`data: {"type":"response.completed","response":{"id":"resp_answer","status":"completed","output":[]}}`,
				))
			}))
			t.Cleanup(up.Close)
			was := provider.CodexBase
			provider.CodexBase = up.URL + CodexPath
			t.Cleanup(func() { provider.CodexBase = was })
			body := `{"model":"gpt-5.5","stream":true,"input":[` +
				`{"type":"compaction","encrypted_content":"existing-checkpoint"},` +
				`{"type":"message","role":"user","content":"earlier request"},` +
				`{"type":"custom_tool_call","name":"exec","call_id":"call_1","input":"earlier command"},` +
				`{"type":"custom_tool_call_output","call_id":"call_1","output":"begin log ` + strings.Repeat("历史工具输出🌳", 60_000) + ` end log"},` +
				`{"type":"message","role":"user","content":"current request"},` +
				`{"type":"custom_tool_call","name":"exec","call_id":"call_2","input":"current command"},` +
				`{"type":"custom_tool_call_output","call_id":"call_2","output":"current output"}]}`
			if tc.path == "/v1/responses" {
				body = strings.Replace(body, "gpt-5.5", "codex/gpt-5.5", 1)
				// An arbitrary native seal is not routable to the pool.
				body = strings.Replace(body, `"type":"compaction","encrypted_content":"existing-checkpoint"`, `"type":"reasoning","summary":[],"encrypted_content":"existing-seal"`, 1)
			}
			rec := relayDo(New().Handler(), "POST", "localhost", tc.path, body, map[string]string{
				"Authorization": "Bearer chatgpt-token", "Chatgpt-Account-Id": "acct-1", "User-Agent": codexUA,
			})
			if !tc.retries {
				mu.Lock()
				defer mu.Unlock()
				if rec.Code != http.StatusBadRequest || len(sent) != 1 || !strings.Contains(rec.Body.String(), "Invalid image") {
					t.Fatalf("unrelated 400 was changed: %d, %d requests, %.300s", rec.Code, len(sent), rec.Body.String())
				}
				return
			}
			want := "answer to the current request"
			if tc.endpoint == "/responses/compact" {
				want = "native-checkpoint"
			}
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("got %d without %q: %.500s", rec.Code, want, rec.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(sent) != 2 || len(sent[0]) <= codexResponsesBodyBudget || len(sent[1]) > codexResponsesBodyBudget {
				t.Fatalf("want an unchanged attempt followed by one smaller retry, got %d requests", len(sent))
			}
			original, retry := gjson.GetBytes(sent[0], "input").Array(), gjson.GetBytes(sent[1], "input").Array()
			if len(original) != 7 || len(retry) != len(original) {
				t.Fatal("the retry changed the number of items")
			}
			for i := range original {
				if i != 3 && !bytes.Equal([]byte(original[i].Raw), []byte(retry[i].Raw)) {
					t.Errorf("retry changed input item %d", i)
				}
			}
			out := retry[3].Get("output").String()
			if !utf8.ValidString(out) || strings.ContainsRune(out, '\uFFFD') || retry[3].Get("call_id").String() != "call_1" || !strings.HasPrefix(out, "begin log") || !strings.HasSuffix(out, "end log") {
				t.Fatal("the retry lost the historical call ID or ends of the log")
			}
		})
	}
}

func TestCodexBodyBudgetPreservesUnfittableInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"role":"user","content":"` + strings.Repeat("x", codexResponsesBodyBudget+100) + `"}]}`)
	if got, ok := codexSmallerHistory(body); ok || !bytes.Equal(got, body) {
		t.Fatal("the latest user input cannot be shortened")
	}
}

func TestCodexHistoryRetryAfterSecondBareRefusalSignalsOverflow(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"detail":"Bad Request"}`)
	})
	body := `{"model":"gpt-5.5","stream":true,"input":[{"role":"user","content":"old"},` +
		`{"type":"function_call","call_id":"call_old","name":"exec","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_old","output":"` + strings.Repeat("old log ", 140_000) +
		`"},{"role":"user","content":"current request"}]}`
	code, reply := codexPost(t, body)
	mu.Lock()
	defer mu.Unlock()
	if code != http.StatusOK || calls != 2 || failedCode(reply) != "context_length_exceeded" {
		t.Fatalf("want one retry and a context overflow, got HTTP %d, %d requests, code %q", code, calls, failedCode(reply))
	}
}

// Older logs can leave the body above budget after the first pass. Keep
// reducing tool text while preserving instructions and the latest request.
func TestCodexBodyBudgetFitsSeveralOlderResults(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"model":"gpt-5.5","input":[{"role":"developer","content":"`)
	body.WriteString(strings.Repeat("x", 780<<10))
	body.WriteString(`"},{"role":"user","content":"first request"}`)
	for i := 0; i < 24; i++ {
		fmt.Fprintf(&body, `,{"type":"function_call","call_id":"call_%d","name":"exec","arguments":"{}"},`, i)
		fmt.Fprintf(&body, `{"type":"function_call_output","call_id":"call_%d","output":[{"type":"input_text","text":"start %s end"}]}`, i, strings.Repeat("log data ", 1500))
	}
	body.WriteString(`,{"role":"user","content":"new request"}]}`)
	fitted, ok := codexSmallerHistory([]byte(body.String()))
	if !ok || len(fitted) > codexResponsesBodyBudget {
		t.Fatalf("the older tool results still exceed the budget: %d bytes", len(fitted))
	}
	before := gjson.Get(body.String(), "input").Array()
	after := gjson.GetBytes(fitted, "input").Array()
	if len(before) != len(after) || before[0].Raw != after[0].Raw || before[len(before)-1].Raw != after[len(after)-1].Raw {
		t.Fatal("the retry lost an item, instructions or the newest request")
	}
	for i := 2; i < len(after)-1; i += 2 {
		out := after[i+1].Get("output.0.text").Str
		if before[i].Raw != after[i].Raw || after[i].Get("call_id").Str != after[i+1].Get("call_id").Str ||
			!strings.HasPrefix(out, "start ") || !strings.HasSuffix(out, " end") {
			t.Fatalf("the retry lost a tool call, pairing or the ends of the log at item %d", i)
		}
	}
}
