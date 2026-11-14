package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// sealedReplay is a ChatGPT-backend-like upstream that seals, as the real
// backend does with Codex's output token replay on, not the reasoning alone
// but each message, call and search as well: it refuses any seal that isn't
// its own with 400 invalid_encrypted_content. Its own name is "acct-one".
type sealedReplay struct {
	mu       sync.Mutex
	requests [][]map[string]any // the input items each request carried
	refused  int                // how many were refused for a foreign seal
	named    bool               // identify the rejected item when its id is available
}

func (s *sealedReplay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var q struct {
		Input []map[string]any `json:"input"`
	}
	json.Unmarshal(b, &q)
	s.mu.Lock()
	s.requests = append(s.requests, q.Input)
	s.mu.Unlock()

	foreign := false
	rejected := "rs_"
	for _, it := range q.Input {
		if enc, _ := it["encrypted_content"].(string); enc != "" && enc != "sealed-by-acct-one" {
			foreign = true
			if id, _ := it["id"].(string); s.named && id != "" {
				rejected = id
			}
		}
	}
	if foreign {
		s.mu.Lock()
		s.refused++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "The encrypted content for item " + rejected + " could not be verified. Reason: Encrypted content could not be decrypted or parsed.", "type": "invalid_request_error", "param": nil, "code": "invalid_encrypted_content"}})
		w.Write(b)
		return
	}
	lead := []string{
		`event: response.created` + "\n" + `data: {"type":"response.created","response":{"id":"r0","model":"gpt-6.1-sol"}}`,
		`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","delta":"from acct-one"}`,
		`event: response.completed` + "\n" + `data: {"type":"response.completed","response":{"id":"r1","usage":{"input_tokens":7,"output_tokens":1}}}`,
	}
	io.WriteString(w, sse(lead...))
}

func (s *sealedReplay) counts() (requests, refused int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests), s.refused
}

// last is the items the last request carried.
func (s *sealedReplay) last() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

// sealsIn reports the sealed items of a request by type, for the assertion
// that what still carries a seal after the retry is only what keeps one.
func sealsIn(items []map[string]any) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		if enc, _ := it["encrypted_content"].(string); enc != "" {
			typ, _ := it["type"].(string)
			out[typ]++
		}
	}
	return out
}

// A Codex conversation on a ChatGPT sign-in whose earlier turns were sealed
// by another account of the same sign-in: with output token replay on, the
// seals are not on the reasoning alone but on every message and call. The
// account asked can't read any of them and answers 400
// invalid_encrypted_content on every turn (hheei, #3887; and the field
// report from this machine, where a Codex conversation failed on every turn
// that carried one).
//
// withoutReasoning alone left the message and call seals in place, so the
// conversation was refused again and the 400 went back to Codex as
// "Codex: Bad Request". Asked again withoutSeals — reasoning whole, every
// other item keeping all but its own encrypted_content — it goes.
func TestForeignSealsOnMessagesAndCallsGoWithout(t *testing.T) {
	fresh(t)
	refusedSeals.Lock()
	refusedSeals.m = map[string]sealsRefused{}
	refusedSeals.Unlock()

	up := &sealedReplay{}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	was := provider.CodexBase
	provider.CodexBase = srv.URL + "/backend-api/codex"
	t.Cleanup(func() { provider.CodexBase = was })
	// Output token replay belongs to a Codex-compatible backend. Other
	// Responses vendors intentionally receive the upstream forVendor shape.
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k", Models: []string{"m"}, Responses: srv.URL + "/v1", CodexAsSent: true}); err != nil {
		t.Fatal(err)
	}
	s := New()
	turn := func(items ...string) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/responses",
			strings.NewReader(`{"model":"relay/m","stream":true,"input":[`+strings.Join(items, ",")+`]}`))
		req.Header.Set("session_id", "thread-sealed-replay")
		req.Header.Set("User-Agent", "codex_cli_rs/0.165.0")
		s.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// what another account's turns left in the history: its sealed reasoning,
	// and — the part withoutReasoning never took out — its sealed message,
	// call and search
	foreign := []string{
		`{"type":"reasoning","id":"rs_a","summary":[],"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"message","id":"msg_a","role":"assistant","content":[{"type":"output_text","text":"done"}],"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"function_call","id":"fc_a","call_id":"c1","name":"shell","arguments":"{}","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"web_search_call","id":"ws_a","action":{"type":"search","query":"go"},"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"function_call_output","call_id":"c1","output":"ok"}`,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}`,
	}

	code, body := turn(foreign...)
	if code != 200 || !strings.Contains(body, "from acct-one") {
		t.Fatalf("after the account switch: %d %s", code, body)
	}
	if _, refused := up.counts(); refused == 0 {
		t.Fatal("the upstream was never refused: the fixture doesn't exercise a foreign seal")
	}

	// what it was finally sent carries no foreign seal, and the message and
	// call are still there to be read: only their seals went
	last := up.last()
	if got := sealsIn(last); len(got) != 0 {
		t.Fatalf("still sent with a seal: %v", got)
	}
	types := map[string]bool{}
	for _, it := range last {
		typ, _ := it["type"].(string)
		types[typ] = true
	}
	for _, want := range []string{"message", "function_call", "web_search_call"} {
		if !types[want] {
			t.Errorf("the %s was dropped whole; only its seal should go (sent %v)", want, types)
		}
	}

	// a call keeps its arguments and id byte for byte: the vendor matches
	// them against the output it already has
	for _, it := range last {
		if typ, _ := it["type"].(string); typ == "function_call" {
			if it["call_id"] != "c1" || it["name"] != "shell" {
				t.Errorf("the call lost its identity: %v", it)
			}
			if it["arguments"] != "{}" {
				t.Errorf("the call lost its arguments: %v", it)
			}
		}
	}

	// The next turn still replays the old account's history, beside what
	// the new account sealed. Only the previously refused seals go up front.
	requests, refused := up.counts()
	own := []string{
		`{"type":"reasoning","id":"rs_own","summary":[],"encrypted_content":"sealed-by-acct-one"}`,
		`{"type":"message","id":"msg_own","role":"assistant","content":[],"encrypted_content":"sealed-by-acct-one"}`,
		`{"type":"function_call","id":"fc_own","call_id":"c2","name":"shell","arguments":"{}","encrypted_content":"sealed-by-acct-one"}`,
		`{"type":"function_call_output","call_id":"c2","output":"ok"}`,
	}
	code, body = turn(append(append([]string{}, foreign...), own...)...)
	if code != http.StatusOK || !strings.Contains(body, "from acct-one") {
		t.Fatalf("next turn failed: status %d", code)
	}
	if got, denied := up.counts(); got != requests+1 || denied != refused {
		t.Fatalf("next turn resent refused seals: %d requests, %d refusals", got-requests, denied-refused)
	}
	for _, typ := range []string{"reasoning", "message", "function_call"} {
		if sealsIn(up.last())[typ] != 1 {
			t.Errorf("the new account's %s seal was lost", typ)
		}
	}
}

// A named refusal is narrower than the whole history, including a foreign
// compaction beside reasoning that the target account can still read.
func TestWithoutUnreadableKeepsOtherSeals(t *testing.T) {
	const body = `{"model":"m","input":[` +
		`{"type":"reasoning","id":"rs_own","encrypted_content":"own-reasoning"},` +
		`{"type":"reasoning","id":"rs_foreign","encrypted_content":"foreign-reasoning"},` +
		`{"type":"compaction","id":"cmp_foreign","encrypted_content":"foreign-compaction"},` +
		`{"type":"message","id":"msg_foreign","role":"assistant","content":[],"encrypted_content":"foreign-message"},` +
		`{"type":"function_call","id":"fc_foreign","call_id":"c1","name":"shell","arguments":"{\"text\":\"kept\"}","encrypted_content":"foreign-call"}]}`
	for _, id := range []string{"rs_foreign", "cmp_foreign", "msg_foreign", "fc_foreign", "rs_absent"} {
		t.Run(id, func(t *testing.T) {
			msg := []byte(`{"error":{"message":"The encrypted content for item ` + id + ` could not be verified.","code":"invalid_encrypted_content"}}`)
			out, changed := withoutUnreadable([]byte(body), msg)
			if id == "rs_absent" {
				if changed {
					t.Fatal("an absent named item caused unrelated seals to be removed")
				}
				return
			}
			if !changed {
				t.Fatal("the named item was not repaired")
			}
			var q struct {
				Input []map[string]any `json:"input"`
			}
			if err := json.Unmarshal(out, &q); err != nil {
				t.Fatal(err)
			}
			for _, it := range q.Input {
				if it["id"] == id {
					if id == "rs_foreign" || id == "cmp_foreign" {
						t.Fatal("the rejected sealed item was kept")
					}
					if _, still := it["encrypted_content"]; still {
						t.Fatal("the named replay seal was kept")
					}
					if id == "fc_foreign" && (it["call_id"] != "c1" || it["arguments"] != `{"text":"kept"}`) {
						t.Fatal("repair changed the call")
					}
				} else if _, sealed := it["encrypted_content"]; !sealed {
					t.Fatal("an unrelated item lost its seal")
				}
			}
			want := 5
			if id == "rs_foreign" || id == "cmp_foreign" {
				want--
			}
			if len(q.Input) != want || !strings.Contains(string(out), "own-reasoning") {
				t.Fatal("repair removed unrelated history")
			}
		})
	}
}

func TestRoutedNamedRefusalKeepsTargetReasoning(t *testing.T) {
	for _, item := range []string{
		`{"type":"message","id":"msg_old","role":"assistant","content":[],"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"function_call","id":"fc_old","call_id":"c1","name":"shell","arguments":"{}","encrypted_content":"sealed-by-acct-two"},{"type":"function_call_output","call_id":"c1","output":"ok"}`,
		`{"type":"compaction","id":"cmp_old","encrypted_content":"sealed-by-acct-two"}`,
	} {
		fresh(t)
		up := &sealedReplay{named: true}
		srv := httptest.NewServer(up)
		t.Cleanup(srv.Close)
		if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "synthetic-key", Models: []string{"m"}, Responses: srv.URL + "/v1"}); err != nil {
			t.Fatal(err)
		}
		s := New()
		for range 2 {
			requests, refused := up.counts()
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"relay/m","stream":true,"input":[`+item+`,{"type":"reasoning","id":"rs_own","summary":[],"encrypted_content":"sealed-by-acct-one"},{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}]}`))
			req.Header.Set("session_id", "named-refusal")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from acct-one") || sealsIn(up.last())["reasoning"] != 1 {
				t.Fatal("a named refusal lost the target account's own reasoning")
			}
			if requests != 0 {
				if got, denied := up.counts(); got != requests+1 || denied != refused {
					t.Fatal("the named rejected seal was resent on the next turn")
				}
			}
		}
	}
}

// Native forwarding must repair just the named item on the first turn and
// remember that repair when Codex replays it beside the target account's seals.
func TestCodexNamedRefusalKeepsTargetSeals(t *testing.T) {
	for _, item := range []string{
		`{"type":"reasoning","id":"rs_old","summary":[],"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"compaction","id":"cmp_old","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"compaction_summary","id":"cmp_old","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"context_compaction","id":"ctx_old","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"message","id":"msg_old","role":"assistant","content":[],"encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"function_call","id":"fc_old","call_id":"c1","name":"shell","arguments":"{\"text\":\"kept\"}","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"custom_tool_call","id":"ctc_old","call_id":"c1","name":"exec","input":"echo kept","encrypted_content":"sealed-by-acct-two"}`,
		`{"type":"web_search_call","id":"ws_old","action":{"type":"search","query":"kept"},"encrypted_content":"sealed-by-acct-two"}`,
	} {
		var rejected map[string]any
		if err := json.Unmarshal([]byte(item), &rejected); err != nil {
			t.Fatal(err)
		}
		input := item
		switch rejected["type"] {
		case "function_call":
			input += `,{"type":"function_call_output","call_id":"c1","output":"ok"}`
		case "custom_tool_call":
			input += `,{"type":"custom_tool_call_output","call_id":"c1","output":"ok"}`
		}
		t.Run(rejected["type"].(string), func(t *testing.T) {
			fresh(t)
			up := &sealedReplay{named: true}
			chatgpt(t, up.ServeHTTP)
			s := New()
			for turn := range 2 {
				requests, refused := up.counts()
				// All four kinds can carry the current account's valid seals.
				body := `{"model":"gpt-6-luna","stream":true,"input":[` + input + `,` +
					`{"type":"reasoning","id":"rs_own","summary":[],"encrypted_content":"sealed-by-acct-one"},` +
					`{"type":"message","id":"msg_own","role":"assistant","content":[],"encrypted_content":"sealed-by-acct-one"},` +
					`{"type":"function_call","id":"fc_own","call_id":"c2","name":"shell","arguments":"{}","encrypted_content":"sealed-by-acct-one"},` +
					`{"type":"web_search_call","id":"ws_own","action":{"type":"search","query":"kept"},"encrypted_content":"sealed-by-acct-one"},` +
					`{"type":"function_call_output","call_id":"c2","output":"ok"},` +
					`{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}]}`
				req := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(body))
				req.Header.Set("session_id", "native-named-refusal")
				req.Header.Set("Authorization", "Bearer synthetic-chatgpt-signin")
				req.Header.Set("chatgpt-account-id", "account-a")
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from acct-one") {
					t.Fatalf("turn %d failed: status %d", turn, rec.Code)
				}
				wantRefusals := 1 - turn
				if got, denied := up.counts(); got != requests+1+wantRefusals || denied != refused+wantRefusals {
					t.Fatalf("turn %d made %d requests and %d refusals; want %d and %d", turn, got-requests, denied-refused, 1+wantRefusals, wantRefusals)
				}
				for _, kind := range []string{"reasoning", "message", "function_call", "web_search_call"} {
					if sealsIn(up.last())[kind] != 1 {
						t.Errorf("turn %d lost the target account's %s seal", turn, kind)
					}
				}
				var found bool
				for _, sent := range up.last() {
					if sent["id"] == rejected["id"] {
						found = true
						if _, sealed := sent["encrypted_content"]; sealed {
							t.Fatal("the rejected item's seal was resent")
						}
						delete(rejected, "encrypted_content")
						want, _ := json.Marshal(rejected)
						got, _ := json.Marshal(sent)
						if string(got) != string(want) {
							t.Fatal("repair changed the rejected item's identity or contents")
						}
					}
				}
				wantItem := true
				switch rejected["type"] {
				case "reasoning", "compaction", "compaction_summary", "context_compaction":
					wantItem = false
				}
				if found != wantItem {
					t.Fatal("repair kept an unusable sealed item or removed a readable message/call")
				}
			}
		})
	}
}

func TestRefusedSealsKeepOtherConversationsAndAccounts(t *testing.T) {
	fresh(t)
	const foreign = `{"input":[{"type":"reasoning","encrypted_content":"old"},{"type":"message","id":"msg_old","encrypted_content":"old"},{"type":"function_call","id":"fc_old","call_id":"c1","arguments":"{}","encrypted_content":"old"}]}`
	const history = `{"input":[{"type":"reasoning","encrypted_content":"old"},{"type":"message","id":"msg_old","encrypted_content":"old"},{"type":"function_call","id":"fc_old","call_id":"c1","arguments":"{}","encrypted_content":"old"},{"type":"compaction","encrypted_content":"old"},{"type":"reasoning","encrypted_content":"new"},{"type":"message","id":"msg_new","encrypted_content":"new"}]}`
	left, ok := withoutSeals([]byte(foreign))
	if !ok {
		t.Fatal("fixture contained no foreign seals to remove")
	}
	refused("route|conversation-a", "account-a", []byte(foreign), left)
	for _, c := range []struct{ conversation, account string }{{"route|conversation-b", "account-a"}, {"route|conversation-a", "account-b"}} {
		if _, changed := withoutRefused(c.conversation, c.account, []byte(history)); changed {
			t.Fatal("refusal memory escaped its conversation or target account")
		}
	}
	out, changed := withoutRefused("route|conversation-a", "account-a", []byte(history))
	if !changed {
		t.Fatal("the target account did not remember its refusal")
	}
	var q struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(out, &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Input) != 5 || q.Input[0]["id"] != "msg_old" || q.Input[1]["id"] != "fc_old" {
		t.Fatal("replay items were removed whole")
	}
	if _, sealed := q.Input[0]["encrypted_content"]; sealed {
		t.Fatal("the old message seal was resent")
	}
	if _, sealed := q.Input[1]["encrypted_content"]; sealed {
		t.Fatal("the old call seal was resent")
	}
	if q.Input[2]["encrypted_content"] != "old" || q.Input[3]["encrypted_content"] != "new" || q.Input[4]["encrypted_content"] != "new" {
		t.Fatal("unrefused compaction or new account seals were changed")
	}
}

// Codex's native backend entrypoint uses the actual sign-in carried by the
// caller, so its refusal memory must not leak into another caller's account.
func TestCodexForeignSealsRememberedPerConversationAndAccount(t *testing.T) {
	fresh(t)
	up := &sealedReplay{}
	chatgpt(t, up.ServeHTTP)
	s := New()
	turn := func(session, account, credential string, own bool) {
		t.Helper()
		items := `{"type":"message","id":"msg_old","role":"assistant","content":[],"encrypted_content":"sealed-by-acct-two"}`
		if own {
			items += `,{"type":"reasoning","id":"rs_own","summary":[],"encrypted_content":"sealed-by-acct-one"}`
		}
		items += `,{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}`
		req := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-6-luna","stream":true,"input":[`+items+`]}`))
		req.Header.Set("session_id", session)
		req.Header.Set("chatgpt-account-id", account)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "from acct-one") {
			t.Fatalf("native turn failed: status %d", rec.Code)
		}
	}
	turn("native-a", "account-a", "synthetic-signin-a", false)
	requests, refused := up.counts()
	turn("native-a", "account-a", "synthetic-signin-a", true)
	if got, denied := up.counts(); got != requests+1 || denied != refused {
		t.Fatal("native next turn resent an already refused seal")
	}
	if sealsIn(up.last())["reasoning"] != 1 {
		t.Fatal("native next turn lost the current account's reasoning")
	}
	for _, c := range []struct{ session, account, credential string }{
		{"native-b", "account-a", "synthetic-signin-a"},
		{"native-a", "account-b", "synthetic-signin-a"},
		{"native-a", "account-a", "synthetic-signin-b"},
	} {
		_, before := up.counts()
		turn(c.session, c.account, c.credential, false)
		if _, denied := up.counts(); denied != before+1 {
			t.Fatal("native refusal memory escaped its conversation or target account")
		}
	}
}

func TestRefusedSealsConcurrentReaders(t *testing.T) {
	fresh(t)
	const body = `{"input":[{"type":"reasoning","encrypted_content":"synthetic-old"}]}`
	left, ok := withoutSeals([]byte(body))
	if !ok {
		t.Fatal("fixture contained no foreign seals to remove")
	}
	refused("route|concurrent", "account", []byte(body), left)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for range 100 {
				refused("route|concurrent", "account", []byte(body), left)
			}
		})
		workers.Go(func() {
			for range 100 {
				if _, changed := withoutRefused("route|concurrent", "account", []byte(body)); !changed {
					t.Error("a concurrent reader lost the remembered refusal")
				}
			}
		})
	}
	workers.Wait()
}

// The seals a conversation keeps are the ones a compaction needs and the
// reasoning withoutSeals takes out whole: a compaction is never left
// without its own seal, so a conversation that was compacted still has its
// summary to go on with.
func TestWithoutSealsKeepsCompactionSeal(t *testing.T) {
	body := []byte(`{"model":"m","input":[` +
		`{"type":"compaction","id":"cmp_1","encrypted_content":"sealed-compaction"},` +
		`{"type":"context_compaction","id":"ctx_1","encrypted_content":"sealed-context"},` +
		`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"sealed-reasoning"},` +
		`{"type":"message","id":"msg_1","role":"assistant","content":[],"encrypted_content":"sealed-message"}]}`)

	out, ok := withoutSeals(body)
	if !ok {
		t.Fatal("withoutSeals changed nothing")
	}
	var q struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(out, &q); err != nil {
		t.Fatal(err)
	}
	kept, dropped := map[string]string{}, 0
	for _, it := range q.Input {
		typ, _ := it["type"].(string)
		if enc, _ := it["encrypted_content"].(string); enc != "" {
			kept[typ] = enc
		} else {
			dropped++
		}
	}
	if kept["compaction"] != "sealed-compaction" || kept["context_compaction"] != "sealed-context" {
		t.Errorf("a compaction lost its seal: %v", kept)
	}
	if _, still := kept["reasoning"]; still {
		t.Error("the reasoning item should go whole, not keep its seal")
	}
	if _, still := kept["message"]; still {
		t.Error("the message should keep all but its seal")
	}
	// the reasoning went whole and the message keeps every other member
	if dropped != 1 {
		t.Errorf("dropped %d items besides the reasoning, want 0", dropped-1)
	}
	// the model and every other member of the request survive
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["model"]) != `"m"` {
		t.Errorf("the request lost its model: %s", top["model"])
	}
}
