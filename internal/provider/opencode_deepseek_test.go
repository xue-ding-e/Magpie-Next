package provider

import (
	"bytes"
	"encoding/json"
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
