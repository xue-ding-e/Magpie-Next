package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

const dsCapturedModel = "deepseek-v4.1-flash-free"

type dsCapturedRequest struct {
	path   string
	header http.Header
	body   map[string]any
}

// The body defaults retain the earlier native DeepSeek profile. The
// client headers, tools and routing follow the current upstream OpenCode.
func dsProfileServer(t *testing.T, reply string) (*Server, func() []dsCapturedRequest) {
	t.Helper()
	fresh(t)
	for _, p := range []provider.Provider{
		{ID: "ds", Name: "DS", Key: "public", Chat: "https://opencode.ai/zen/v1", Models: []string{dsCapturedModel}},
		{ID: "other", Name: "Other", Key: "other-key", Chat: "https://other.test/v1", Models: []string{"gpt-6.1-sol"}},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var got []dsCapturedRequest
	s := New()
	s.client = &http.Client{Transport: countTransport(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body.Close()
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		mu.Lock()
		got = append(got, dsCapturedRequest{path: r.URL.String(), header: r.Header.Clone(), body: m})
		mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})}
	return s, func() []dsCapturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]dsCapturedRequest(nil), got...)
	}
}

func TestOpenCodeDeepSeekClientProtocols(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, proto := range []string{"chat", "responses", "messages"} {
			t.Run(fmt.Sprintf("%s/stream=%v", proto, stream), func(t *testing.T) {
				s, captured := dsProfileServer(t, chatSSE("OPENCODE_CAPTURE_OK", "", ""))
				path, content := "/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`
				budget := float64(32000)
				switch proto {
				case "responses":
					path, content = "/v1/responses", `"input":"hello"`
				case "messages":
					path, content, budget = "/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"hello"}]`, 128
				}
				body := fmt.Sprintf(`{"model":"ds/%s","stream":%v,%s}`, dsCapturedModel, stream, content)
				rec := zenSend(t, s, path, body)
				if !strings.Contains(rec.Body.String(), "OPENCODE_CAPTURE_OK") {
					t.Fatalf("reply: %s", rec.Body)
				}
				got := captured()
				if len(got) != 1 {
					t.Fatalf("upstream requests: %d", len(got))
				}
				x := got[0]
				if x.path != "https://opencode.ai/inference/openai/v1/chat/completions" {
					t.Errorf("path: %s", x.path)
				}
				if x.header.Get("User-Agent") != provider.OpenCodeUserAgent() || x.header.Get("x-opencode-client") != "cli" {
					t.Errorf("client: %v", x.header)
				}
				id := x.header.Get("x-opencode-session")
				for _, h := range []string{"x-opencode-session-id", "x-session-affinity", "x-session-id"} {
					if id == "" || x.header.Get(h) != id {
						t.Errorf("%s: %q, session %q", h, x.header.Get(h), id)
					}
				}
				if x.header.Get("x-opencode-request") != "" {
					t.Error("obsolete request header")
				}
				if x.body["max_completion_tokens"] != budget || x.body["max_tokens"] != nil || x.body["reasoning_effort"] != "max" {
					t.Errorf("parameters: %v", x.body)
				}
				if x.body["stream"] != true || x.body["stream_options"].(map[string]any)["include_usage"] != true {
					t.Error("upstream must stream with usage")
				}
				if names := strings.Join(toolNames(x.body), ","); names != strings.Join(provider.OpenCodeTools, ",") {
					t.Errorf("tools: %s", names)
				}
				for _, tool := range x.body["tools"].([]any) {
					fn := tool.(map[string]any)["function"].(map[string]any)
					if fn["strict"] != false {
						t.Errorf("strict: %v", fn)
					}
				}
			})
		}
	}
}

func TestOpenCodeDeepSeekKeepsClientToolContract(t *testing.T) {
	s, captured := dsProfileServer(t, chatSSE("Reading.", "read", `{"file_path":"example.json"}`))
	body := `{"model":"ds/deepseek-v4.1-flash-free","max_tokens":512,"messages":[{"role":"user","content":"read"}],"tools":[{"name":"Read","description":"Client reader","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}]}`
	rec := zenSend(t, s, "/v1/messages", body)
	if !strings.Contains(rec.Body.String(), `"name":"Read"`) || !strings.Contains(rec.Body.String(), `"file_path":"example.json"`) {
		t.Fatalf("client tool was changed: %s", rec.Body)
	}
	got := captured()
	for _, tool := range got[0].body["tools"].([]any) {
		fn := tool.(map[string]any)["function"].(map[string]any)
		if fn["name"] == "read" {
			schema := fn["parameters"].(map[string]any)
			props := schema["properties"].(map[string]any)
			if props["file_path"] == nil || props["path"] != nil {
				t.Fatalf("client schema changed: %v", schema)
			}
		}
	}
}

func TestOpenCodeDeepSeekDoesNotCaptureOtherModels(t *testing.T) {
	s, captured := dsProfileServer(t, chatSSE("OTHER_OK", "", ""))
	zenSend(t, s, "/v1/chat/completions", `{"model":"other/gpt-6.1-sol","messages":[{"role":"user","content":"hello"}]}`)
	got := captured()
	if len(got) != 1 || got[0].path != "https://other.test/v1/chat/completions" {
		t.Fatalf("wrong destination: %+v", got)
	}
	if got[0].header.Get("x-opencode-session") != "" || got[0].header.Get("x-opencode-client") != "" {
		t.Fatal("DS headers leaked to another model")
	}
	if got[0].body["tools"] != nil || got[0].body["reasoning_effort"] != nil || got[0].body["max_completion_tokens"] != nil {
		t.Fatalf("DS parameters leaked: %v", got[0].body)
	}
	for _, model := range []string{"gpt-unknown", "group/ds"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hello"}]}`))
		s.Handler().ServeHTTP(rec, req)
		if rec.Code == 200 {
			t.Errorf("unselected model %q unexpectedly served", model)
		}
	}
	if len(captured()) != 1 {
		t.Fatal("an unselected model reached DS")
	}
}

func TestOpenCodeDeepSeekUnavailableStubIsNotClientTool(t *testing.T) {
	s, _ := dsProfileServer(t, chatSSE("", "read", `{"path":"example.json"}`))
	rec := zenSend(t, s, "/v1/chat/completions", `{"model":"ds/deepseek-v4.1-flash-free","messages":[{"role":"user","content":"hello"}]}`)
	if strings.Contains(rec.Body.String(), "tool_calls") || !strings.Contains(rec.Body.String(), zenStubNote) {
		t.Fatalf("unavailable tool reached client: %s", rec.Body)
	}
}
