package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A probe checks the final body, not just the logical model reported in
// its result: a saved upstream rename must not bypass the allowlist.
func TestProbeOutboundModelPolicy(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	var requests atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(fake.Close)
	var p = Provider{ID: "probe-policy", Key: "key", Chat: fake.URL}
	for _, model := range []string{"gpt-6-sol", "gpt-6.1-sol"} {
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{}})
		result := probe(t.Context(), p, Chat, fake.URL, body, "gpt-6.1-sol", time.Second)
		if model == "gpt-6-sol" && result.Error == "" {
			t.Error("a probe sent a forbidden upstream rename")
		}
		if model == "gpt-6.1-sol" && (!result.OK || result.Error != "") {
			t.Errorf("allowed probe: %+v", result)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream received %d probes, want only the allowed one", got)
	}
}

func TestProbeGeminiURLModelPolicy(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	var requests atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	t.Cleanup(fake.Close)
	var p = Provider{ID: "gemini-policy", Key: "key", Gemini: fake.URL}
	for _, sent := range []string{"gpt-6-sol", "gpt-6.1-sol"} {
		result := probe(t.Context(), p, Gemini, fake.URL+GeminiPath(sent, false), []byte(`{"contents":[]}`), "gpt-6.1-sol", time.Second)
		if sent == "gpt-6-sol" && result.Error == "" {
			t.Error("a model in Gemini's URL bypassed the policy")
		}
		if sent == "gpt-6.1-sol" && (!result.OK || result.Error != "") {
			t.Errorf("allowed Gemini probe: %+v", result)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream received %d probes, want only the allowed URL", got)
	}
}

func TestAskSystemOneOutboundModelPolicy(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	var requests atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "gpt-6.1-sol" {
			t.Errorf("decision probe sent forbidden model %q", body.Model)
		}
		io.WriteString(w, `{"answers":{"ok":true}}`)
	}))
	t.Cleanup(fake.Close)
	var p = Provider{ID: "decision-policy", Key: "key", Decide: fake.URL}
	if err := p.AskSystemOne(t.Context(), "gpt-6-sol"); err == nil {
		t.Error("a decision probe bypassed the model policy")
	}
	if err := p.AskSystemOne(t.Context(), "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream received %d questions, want only the allowed one", got)
	}
}
