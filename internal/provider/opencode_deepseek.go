package provider

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
)

// OpenCodeDeepSeekFlashFree is the Zen model whose Chat defaults were
// captured from OpenCode 2.1.3 on 2026-10-23. Headers, paths, environment
// and tools follow the current OpenCode client in opencode_client.go.
const OpenCodeDeepSeekFlashFree = "deepseek-v4.1-flash-free"

// OpenCodeDeepSeek reports whether this model uses Zen's DeepSeek Chat
// profile. Go, custom upstreams and other models keep their own behavior.
func (p Provider) OpenCodeDeepSeek(model string) bool {
	if model != OpenCodeDeepSeekFlashFree {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(p.Chat))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && openCodeGateway(p.Chat) == "zen"
}

// OpenCodeDeepSeekBody fills the native Chat defaults without changing the
// caller's messages, schemas, sampling settings or explicit effort/budget.
// Gateway translations set their default budget before fitting the context;
// the default here also covers direct provider probes.
func (p Provider) OpenCodeDeepSeekBody(proto Protocol, body []byte) []byte {
	if proto != Chat || !p.OpenCodeDeepSeek(OpenCodeDeepSeekFlashFree) {
		return body
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}
	var model string
	if json.Unmarshal(fields["model"], &model) != nil || !p.OpenCodeDeepSeek(model) {
		return body
	}
	fields["stream"] = json.RawMessage("true")
	var options map[string]json.RawMessage
	if json.Unmarshal(fields["stream_options"], &options) != nil || options == nil {
		options = make(map[string]json.RawMessage)
	}
	options["include_usage"] = json.RawMessage("true")
	encoded, err := openCodeDeepSeekJSON(options)
	if err != nil {
		return body
	}
	fields["stream_options"] = encoded
	if _, has := fields["max_completion_tokens"]; !has {
		if limit, has := fields["max_tokens"]; has {
			fields["max_completion_tokens"] = limit
		} else {
			fields["max_completion_tokens"] = json.RawMessage("32000")
		}
	}
	delete(fields, "max_tokens")
	if _, has := fields["reasoning_effort"]; !has {
		fields["reasoning_effort"] = json.RawMessage(`"max"`)
	}
	var tools []json.RawMessage
	if json.Unmarshal(fields["tools"], &tools) == nil {
		changed := false
		for i, raw := range tools {
			var tool, function map[string]json.RawMessage
			if json.Unmarshal(raw, &tool) != nil || json.Unmarshal(tool["function"], &function) != nil || function == nil {
				continue
			}
			if _, has := function["strict"]; has {
				continue
			}
			function["strict"] = json.RawMessage("false")
			if tool["function"], err = openCodeDeepSeekJSON(function); err != nil {
				return body
			}
			if tools[i], err = openCodeDeepSeekJSON(tool); err != nil {
				return body
			}
			changed = true
		}
		if changed {
			if fields["tools"], err = openCodeDeepSeekJSON(tools); err != nil {
				return body
			}
		}
	}
	out, err := openCodeDeepSeekJSON(fields)
	if err != nil {
		return body
	}
	return out
}

// Raw messages keep user numbers and JSON values intact. Disabling HTML
// escaping also leaves prompt text and schema strings in their own spelling.
func openCodeDeepSeekJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
