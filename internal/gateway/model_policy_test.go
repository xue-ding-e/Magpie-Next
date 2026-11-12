package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

func allowOnly(t *testing.T, model string) {
	t.Helper()
	s := settings.Load()
	s.AllowedModels = []string{model}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
}

func TestModelPolicyBlocksNativeInferenceBeforeNetwork(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	var calls atomic.Int32
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"id":"allowed"}`)
	})
	allowOnly(t, "gpt-6.1-sol")
	for _, path := range []string{"/responses", "/responses/compact"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", CodexPath+path, strings.NewReader(`{"model":"gpt-6-sol","input":[]}`))
		r.Header.Set("Authorization", "Bearer local-test-token")
		New().Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || calls.Load() != 0 {
			t.Fatalf("%s: status=%d upstream calls=%d", path, w.Code, calls.Load())
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-6.1-sol","input":[]}`))
	r.Header.Set("Authorization", "Bearer local-test-token")
	New().Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("allowed native model: status=%d upstream calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
}

func TestModelPolicyBlocksFallbackBeforeNetwork(t *testing.T) {
	plan := &fake{t: t, ctype: "application/json", code: 429, reply: `{"error":{"message":"rate limited"}}`}
	spare := &fake{t: t, ctype: "application/json", reply: `{"id":"forbidden-model","choices":[]}`}
	twoProviders(t, plan, spare)
	allowOnly(t, "m1")
	code, body := post(t, "/v1/chat/completions", chatReq)
	if code == http.StatusOK || len(spare.got) != 0 {
		t.Fatalf("forbidden fallback was used: status=%d body=%s upstream=%s", code, body, spare.got)
	}
}

func TestModelPolicyDenialDoesNotRestAccount(t *testing.T) {
	plan := &fake{t: t, ctype: "application/json", reply: `{"choices":[]}`}
	spare := &fake{t: t, ctype: "application/json", reply: `{"id":"allowed-spare","choices":[]}`}
	twoProviders(t, plan, spare)
	allowOnly(t, "m2")
	code, body := post(t, "/v1/chat/completions", chatReq)
	if code != http.StatusOK || !strings.Contains(body, "allowed-spare") || len(plan.got) != 0 {
		t.Fatalf("allowed model unavailable after local denial: %d %s", code, body)
	}
	restingUntil.Lock()
	defer restingUntil.Unlock()
	if len(restingUntil.m) != 0 {
		t.Fatalf("local policy denial rested an account: %v", restingUntil.m)
	}
}

func TestModelPolicyChecksFinalWireModel(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	allowOnly(t, "gpt-6.1-sol")
	p := provider.Provider{ID: "relay", Key: "test", Chat: up.URL + "/v1"}
	_, err := New().forwardOnce(context.Background(), p, provider.Chat, "/chat/completions", []byte(`{"model":"gpt-6-sol","messages":[]}`), http.Header{})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("final wire model bypassed policy: err=%v calls=%d", err, calls.Load())
	}
}

func TestModelPolicyBlocksDirectDecisionBeforeNetwork(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"answers":{}}`)
	}))
	defer up.Close()
	allowOnly(t, "gpt-6.1-sol")
	p := provider.Provider{ID: "decision", Key: "test", Decide: up.URL + "/v1"}
	_, err := New().postDecideSent(context.Background(), p, "gpt-6-sol", []byte(`{"model":"gpt-6-sol","questions":{}}`))
	if err == nil || !strings.Contains(err.Error(), "allowedModels") || calls.Load() != 0 {
		t.Fatalf("direct decision bypassed policy: err=%v calls=%d", err, calls.Load())
	}
}

func TestModelPolicyChecksGeminiURL(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"candidates":[]}`)
	}))
	defer up.Close()
	allowOnly(t, "gpt-6.1-sol")
	p := provider.Provider{ID: "gemini", Key: "test", Gemini: up.URL}
	s := New()
	res, err := s.forwardOnce(context.Background(), p, provider.Gemini, "/publishers/google/models/gpt-6.1-sol:streamGenerateContent?alt=sse", []byte(`{"contents":[]}`), http.Header{})
	if err != nil {
		t.Fatalf("allowed model in Gemini URL was refused: %v", err)
	}
	res.Body.Close()
	_, err = s.forwardOnce(context.Background(), p, provider.Gemini, "/models/gpt-6-sol:generateContent", []byte(`{"model":"gpt-6.1-sol","contents":[]}`), http.Header{})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("Gemini wire model bypassed policy: err=%v calls=%d", err, calls.Load())
	}
}

func TestModelPolicyChangesNativeListAndETag(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"native"`)
		io.WriteString(w, `{"models":[{"slug":"gpt-6.1-sol"},{"slug":"gpt-6-sol"},{"slug":"codex-auto-review","visibility":"hide"}]}`)
	})
	ask := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		New().Handler().ServeHTTP(w, httptest.NewRequest("GET", CodexPath+"/models", nil))
		return w
	}
	allowOnly(t, "gpt-6.1-sol")
	first := ask()
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"gpt-6.1-sol"`) || strings.Contains(first.Body.String(), `"gpt-6-sol"`) || strings.Contains(first.Body.String(), `"codex-auto-review"`) {
		t.Fatalf("strict model list: %d %s", first.Code, first.Body.String())
	}
	allowOnly(t, "gpt-6-sol")
	second := ask()
	if second.Header().Get("ETag") == first.Header().Get("ETag") || !strings.Contains(second.Body.String(), `"gpt-6-sol"`) || strings.Contains(second.Body.String(), `"gpt-6.1-sol"`) {
		t.Fatalf("changed policy did not refresh model choices: %d %s", second.Code, second.Body.String())
	}
}
