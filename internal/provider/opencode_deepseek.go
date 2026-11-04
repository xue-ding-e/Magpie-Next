package provider

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// OpenCodeDeepSeekFlashFree is the Zen model served by OpenCode's desktop
// Chat API, captured from OpenCode 2.1.3 on 2026-10-23.
const OpenCodeDeepSeekFlashFree = "deepseek-v4.1-flash-free"

// OpenCodeDeepSeekTools are the two native tools verified with the desktop
// API. Missing tools can be offered as stubs and filtered by the gateway.
var OpenCodeDeepSeekTools = []string{"shell", "read"}

// OpenCodeDeepSeekToolSchemas are the unmodified parameter schemas captured
// from OpenCode 2.1.3 desktop's successful request on 2026-10-23.
var OpenCodeDeepSeekToolSchemas = map[string]string{
	"shell": `{"type":"object","properties":{"command":{"type":"string","description":"Shell command string to execute"},"workdir":{"type":"string","description":"Working directory to execute the command in. Defaults to the current working directory. When possible, avoid changing directories in the command and set the working directory here instead."},"timeout":{"type":"integer","minimum":0,"description":"Timeout in milliseconds. Set to 0 to disable the timeout. Defaults to 120000 for foreground commands. Background commands have no timeout by default."},"background":{"type":"boolean","description":"Run the command in the background and return immediately (useful for dev servers and long-running builds). You do not need to use '&' at the end of the command when using this parameter. You will be notified when it completes. DO NOT poll for completion."}},"required":["command"],"additionalProperties":false}`,
	"read":  `{"type":"object","properties":{"path":{"type":"string","description":"File or directory to read"},"offset":{"type":"integer","minimum":0,"description":"The line or directory entry to start reading from (1-based)"},"limit":{"type":"integer","minimum":0,"description":"The maximum number of lines or directory entries to read (defaults to and capped at 2000)"}},"required":["path"],"additionalProperties":false}`,
}

// OpenCodeDeepSeek reports whether this model uses Zen's desktop DeepSeek
// profile. Go, custom upstreams and other models keep their own behavior.
func (p Provider) OpenCodeDeepSeek(model string) bool {
	if model != OpenCodeDeepSeekFlashFree {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(p.Chat))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && openCodeGateway(p.Chat) == "zen"
}

// OpenCodeDeepSeekURL moves a standard Chat base or endpoint to the
// desktop API. Custom paths, other APIs and the URL's query stay as given.
func (p Provider) OpenCodeDeepSeekURL(u string, proto Protocol, model string) string {
	if proto != Chat || !p.OpenCodeDeepSeek(model) {
		return u
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || openCodeGateway(u) != "zen" {
		return u
	}
	trailing := strings.HasSuffix(pu.EscapedPath(), "/")
	path := strings.TrimSuffix(pu.EscapedPath(), "/")
	switch path {
	case "/zen/v1", "/inference/v1":
		pu.Path = "/inference/openai/v1"
	case "/zen/v1/chat/completions", "/inference/v1/chat/completions":
		pu.Path = "/inference/openai/v1/chat/completions"
	default:
		return u
	}
	if trailing {
		pu.Path += "/"
	}
	pu.RawPath = ""
	return pu.String()
}

// OpenCodeDeepSeekHeaders gives this model the desktop client's headers.
// A conversation keeps its session; each request gets a fresh trace.
func (p Provider) OpenCodeDeepSeekHeaders(h http.Header, seed, directory, model string) {
	if h == nil || !p.OpenCodeDeepSeek(model) {
		return
	}
	OpenCodeClient(h, seed)
	h.Set("User-Agent", "opencode/latest/2.1.3/desktop")
	h.Set("x-opencode-client", "desktop")
	session := h.Get("x-opencode-session")
	h.Set("x-session-affinity", session)
	h.Set("x-session-id", session)
	h.Del("x-opencode-request")
	if project := openCodeDeepSeekProject(directory); project != "" {
		h.Set("x-opencode-project", project)
	}
	var trace [32]byte // trace id, span id and the B3 parent span id
	if _, err := rand.Read(trace[:]); err != nil {
		h.Del("traceparent")
		h.Del("b3")
		return
	}
	tid := hex.EncodeToString(trace[:16])
	span := hex.EncodeToString(trace[16:24])
	parent := hex.EncodeToString(trace[24:])
	h.Set("traceparent", "00-"+tid+"-"+span+"-01")
	h.Set("b3", tid+"-"+span+"-1-"+parent)
}

// openCodeDeepSeekProject follows the desktop client's directory identity,
// not a git commit or a hash of the unnormalized spelling of the path.
func openCodeDeepSeekProject(directory string) string {
	if directory == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		directory = wd
	}
	resolved, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	id := sha1.Sum([]byte("directory:" + resolved))
	return hex.EncodeToString(id[:])
}

// OpenCodeDeepSeekBody applies desktop Chat defaults without changing the
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
