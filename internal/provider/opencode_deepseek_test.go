package provider

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestOpenCodeDeepSeekScope(t *testing.T) {
	var tests = []struct {
		name, chat, model string
		want              bool
	}{
		{"zen", "https://opencode.ai/zen/v1", OpenCodeDeepSeekFlashFree, true},
		{"inference", "https://opencode.ai/inference/v1", OpenCodeDeepSeekFlashFree, true},
		{"native", "https://opencode.ai/inference/openai/v1", OpenCodeDeepSeekFlashFree, true},
		{"go", "https://opencode.ai/zen/go/v1", OpenCodeDeepSeekFlashFree, false},
		{"inference go", "https://opencode.ai/inference/go/openai/v1", OpenCodeDeepSeekFlashFree, false},
		{"custom", "https://opencode.ai/inference/custom/v1", OpenCodeDeepSeekFlashFree, false},
		{"other host", "https://other.example/zen/v1", OpenCodeDeepSeekFlashFree, false},
		{"lookalike host", "https://opencode.ai.example/zen/v1", OpenCodeDeepSeekFlashFree, false},
		{"paid", "https://opencode.ai/zen/v1", "deepseek-v4.1-flash", false},
		{"other free", "https://opencode.ai/zen/v1", "kimi-k2.6-free", false},
		{"case matters", "https://opencode.ai/zen/v1", "DEEPSEEK-V4.1-FLASH-FREE", false},
		{"no chat", "", OpenCodeDeepSeekFlashFree, false},
		{"relative", "//opencode.ai/zen/v1", OpenCodeDeepSeekFlashFree, false},
		{"not http", "file://opencode.ai/zen/v1", OpenCodeDeepSeekFlashFree, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p = Provider{Chat: tt.chat, Anthropic: "https://opencode.ai/zen"}
			if got := p.OpenCodeDeepSeek(tt.model); got != tt.want {
				t.Fatalf("profile = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenCodeDeepSeekURL(t *testing.T) {
	var p = Provider{Chat: "https://opencode.ai/zen/v1"}
	var tests = []struct {
		url, want string
	}{
		{"https://opencode.ai/zen/v1", "https://opencode.ai/inference/openai/v1"},
		{"https://opencode.ai/zen/v1/", "https://opencode.ai/inference/openai/v1/"},
		{"https://opencode.ai/inference/v1", "https://opencode.ai/inference/openai/v1"},
		{"https://opencode.ai/zen/v1/chat/completions?x=%2f&x=a+b", "https://opencode.ai/inference/openai/v1/chat/completions?x=%2f&x=a+b"},
		{"https://opencode.ai/inference/v1/chat/completions", "https://opencode.ai/inference/openai/v1/chat/completions"},
		{"https://opencode.ai/inference/openai/v1/chat/completions?x=%2f", ""},
		{"https://opencode.ai/zen/go/v1/chat/completions", ""},
		{"https://opencode.ai/inference/custom/v1/chat/completions", ""},
		{"https://opencode.ai/zen/v1/custom/chat/completions", ""},
		{"https://opencode.ai/zen/v1/responses", ""},
		{"https://other.example/zen/v1", ""},
		{"https://opencode.ai/zen/%76%31", ""},
		{"://bad", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			want := tt.want
			if want == "" {
				want = tt.url
			}
			if got := p.OpenCodeDeepSeekURL(tt.url, Chat, OpenCodeDeepSeekFlashFree); got != want {
				t.Fatalf("URL = %q, want %q", got, want)
			}
			for _, proto := range []Protocol{Responses, Anthropic, Gemini} {
				if got := p.OpenCodeDeepSeekURL(tt.url, proto, OpenCodeDeepSeekFlashFree); got != tt.url {
					t.Errorf("%s changed URL to %q", proto, got)
				}
			}
			if got := p.OpenCodeDeepSeekURL(tt.url, Chat, "deepseek-v4.1-flash"); got != tt.url {
				t.Errorf("paid model changed URL to %q", got)
			}
		})
	}
}

func TestOpenCodeDeepSeekHeaders(t *testing.T) {
	var p = Provider{Chat: "https://opencode.ai/zen/v1"}
	directory := filepath.Join(t.TempDir(), "Mixed Case", "..", "Workspace")
	resolved, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	project := sha1.Sum([]byte("directory:" + resolved))
	h := make(http.Header)
	h.Set("X-Unrelated", "keep")
	p.OpenCodeDeepSeekHeaders(h, "deepseek-headers-test", directory, OpenCodeDeepSeekFlashFree)
	if h.Get("User-Agent") != "opencode/latest/2.1.3/desktop" || h.Get("x-opencode-client") != "desktop" {
		t.Fatalf("desktop headers = %v", h)
	}
	if h.Get("x-opencode-project") != hex.EncodeToString(project[:]) || h.Get("X-Unrelated") != "keep" {
		t.Fatalf("project or unrelated header = %v", h)
	}
	session := h.Get("x-opencode-session")
	if !openCodeRealSession(session) {
		t.Fatalf("session = %q", session)
	}
	for _, name := range []string{"x-opencode-session-id", "x-session-affinity", "x-session-id"} {
		if h.Get(name) != session {
			t.Errorf("%s = %q, want session %q", name, h.Get(name), session)
		}
	}
	if h.Get("x-opencode-request") != "" {
		t.Error("desktop request has x-opencode-request")
	}
	trace := h.Get("traceparent")
	b3 := h.Get("b3")
	if !regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`).MatchString(trace) ||
		!regexp.MustCompile(`^[0-9a-f]{32}-[0-9a-f]{16}-1-[0-9a-f]{16}$`).MatchString(b3) {
		t.Fatalf("traceparent = %q, b3 = %q", trace, b3)
	}
	tr, b := strings.Split(trace, "-"), strings.Split(b3, "-")
	if tr[1] != b[0] || tr[2] != b[1] {
		t.Fatalf("trace context differs: %q / %q", trace, b3)
	}
	p.OpenCodeDeepSeekHeaders(h, "deepseek-headers-test", directory, OpenCodeDeepSeekFlashFree)
	if h.Get("x-opencode-session") != session || h.Get("traceparent") == trace || h.Get("b3") == b3 {
		t.Fatalf("conversation must keep its session and renew its trace: %v", h)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		wd = real
	}
	project = sha1.Sum([]byte("directory:" + wd))
	p.OpenCodeDeepSeekHeaders(h, session, "", OpenCodeDeepSeekFlashFree)
	if h.Get("x-opencode-session") != session || h.Get("x-opencode-project") != hex.EncodeToString(project[:]) {
		t.Fatalf("empty directory did not use cwd: %v", h)
	}
}

func TestOpenCodeDeepSeekProjectFollowsSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-project")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	var p = Provider{Chat: "https://opencode.ai/zen/v1"}
	direct, linked := make(http.Header), make(http.Header)
	p.OpenCodeDeepSeekHeaders(direct, "deepseek-symlink", target, OpenCodeDeepSeekFlashFree)
	p.OpenCodeDeepSeekHeaders(linked, "deepseek-symlink", link, OpenCodeDeepSeekFlashFree)
	if direct.Get("x-opencode-project") != linked.Get("x-opencode-project") {
		t.Fatalf("same real directory has different projects: %q / %q", direct.Get("x-opencode-project"), linked.Get("x-opencode-project"))
	}
}

func TestOpenCodeDeepSeekUnrelatedUnchanged(t *testing.T) {
	var providers = []Provider{
		{Chat: "https://api.deepseek.com/v1"},
		{Chat: "https://opencode.ai/zen/go/v1"},
		{Chat: "https://opencode.ai/inference/custom/v1"},
		{Chat: "https://opencode.ai/zen/v1"},
	}
	for i, p := range providers {
		model := OpenCodeDeepSeekFlashFree
		if i == len(providers)-1 {
			model = "gpt-6.1-sol"
		}
		h := make(http.Header)
		h.Set("User-Agent", "caller")
		h.Set("x-opencode-request", "keep")
		before := h.Clone()
		p.OpenCodeDeepSeekHeaders(h, "seed", t.TempDir(), model)
		if !reflect.DeepEqual(h, before) {
			t.Errorf("%s/%s changed headers: %v", p.Chat, model, h)
		}
		body := []byte(`{"model":"` + model + `", "messages":[], "stream":false}`)
		if got := p.OpenCodeDeepSeekBody(Chat, body); !bytes.Equal(got, body) {
			t.Errorf("%s/%s changed body: %s", p.Chat, model, got)
		}
		const target = "https://opencode.ai/zen/v1/chat/completions"
		if got := p.OpenCodeDeepSeekURL(target, Chat, model); got != target {
			t.Errorf("%s/%s changed URL: %s", p.Chat, model, got)
		}
	}
}

func TestOpenCodeDeepSeekBodyDefaults(t *testing.T) {
	var p = Provider{Chat: "https://opencode.ai/zen/v1"}
	body := []byte(`{"model":"deepseek-v4.1-flash-free","messages":[],"stream":false,"stream_options":{"custom":42},"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}]}`)
	var got struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
			Custom       int  `json:"custom"`
		} `json:"stream_options"`
		MaxTokens int    `json:"max_completion_tokens"`
		Effort    string `json:"reasoning_effort"`
		Tools     []struct {
			Function struct {
				Strict *bool `json:"strict"`
			} `json:"function"`
		} `json:"tools"`
	}
	out := p.OpenCodeDeepSeekBody(Chat, body)
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Stream || !got.StreamOptions.IncludeUsage || got.StreamOptions.Custom != 42 || got.MaxTokens != 32000 || got.Effort != "max" {
		t.Fatalf("desktop defaults = %s", out)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Strict == nil || *got.Tools[0].Function.Strict {
		t.Fatalf("function strict must default to false: %s", out)
	}
	for _, proto := range []Protocol{Responses, Anthropic, Gemini} {
		if changed := p.OpenCodeDeepSeekBody(proto, body); !bytes.Equal(changed, body) {
			t.Errorf("%s changed body: %s", proto, changed)
		}
	}
}

func TestOpenCodeDeepSeekBodyKeepsUserValues(t *testing.T) {
	var p = Provider{Chat: "https://opencode.ai/inference/openai/v1"}
	for _, effort := range []string{"low", "high", "none"} {
		body := []byte(`{"model":"deepseek-v4.1-flash-free","max_tokens":1234,"reasoning_effort":"` + effort + `","temperature":0.125,"top_p":0.85,"seed":9007199254740993,"messages":[{"role":"user", "content":"keep <&> \\path"}],"tools":[{"type":"function","function":{"name":"read","strict":true,"parameters":{"type":"object","properties":{"file_path":{"type":"string","enum":["<&>"]}}}}}]}`)
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal(body, &before); err != nil {
			t.Fatal(err)
		}
		out := p.OpenCodeDeepSeekBody(Chat, body)
		if err := json.Unmarshal(out, &after); err != nil {
			t.Fatal(err)
		}
		if string(after["max_completion_tokens"]) != "1234" || after["max_tokens"] != nil || string(after["reasoning_effort"]) != `"`+effort+`"` {
			t.Fatalf("caller budget or effort changed: %s", out)
		}
		for _, key := range []string{"messages", "tools", "temperature", "top_p", "seed"} {
			var want, got bytes.Buffer
			if err := json.Compact(&want, before[key]); err != nil {
				t.Fatal(err)
			}
			if err := json.Compact(&got, after[key]); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Errorf("%s changed: %s, want %s", key, got.Bytes(), want.Bytes())
			}
		}
	}
	const both = `{"model":"deepseek-v4.1-flash-free","max_tokens":1234,"max_completion_tokens":5678,"tools":[{"type":"function","function":{"name":"read","strict":false}}]}`
	var got map[string]json.RawMessage
	if err := json.Unmarshal(p.OpenCodeDeepSeekBody(Chat, []byte(both)), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["max_completion_tokens"]) != "5678" || got["max_tokens"] != nil {
		t.Fatalf("max_completion_tokens must win: %s", got)
	}
}

func TestOpenCodeDeepSeekBodyInvalidUnchanged(t *testing.T) {
	var p = Provider{Chat: "https://opencode.ai/zen/v1"}
	for _, body := range []string{`{`, `null`, `[]`, `"text"`, `{"model":"deepseek-v4.1-flash-free"} trailing`} {
		if got := p.OpenCodeDeepSeekBody(Chat, []byte(body)); string(got) != body {
			t.Errorf("invalid object %q became %q", body, got)
		}
	}
}

func TestOpenCodeDeepSeekNativeToolSchemas(t *testing.T) {
	if !reflect.DeepEqual(OpenCodeDeepSeekTools, []string{"shell", "read"}) {
		t.Fatalf("native tools = %v", OpenCodeDeepSeekTools)
	}
	for _, name := range OpenCodeDeepSeekTools {
		var schema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required             []string `json:"required"`
			AdditionalProperties *bool    `json:"additionalProperties"`
		}
		if err := json.Unmarshal([]byte(OpenCodeDeepSeekToolSchemas[name]), &schema); err != nil {
			t.Fatal(err)
		}
		field := "command"
		if name == "read" {
			field = "path"
		}
		if schema.Type != "object" || !reflect.DeepEqual(schema.Required, []string{field}) || schema.Properties[field].Type != "string" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("%s does not have its native contract: %s", name, OpenCodeDeepSeekToolSchemas[name])
		}
	}
}
