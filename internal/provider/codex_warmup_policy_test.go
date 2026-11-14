package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// The warm-up's cheapest model must come from the allowed models, even
// when the vendor still lists a cheaper Luna beside the user's sole pick.
func TestCodexWarmModelOnlyAllowed(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	const user = "warm-policy@example.com"
	var models = []catalog.Model{
		{ID: "gpt-6-luna", Price: &catalog.Price{Input: 0.1, Output: 0.5}},
		{ID: "gpt-6.1-sol", Price: &catalog.Price{Input: 2, Output: 10}, Efforts: []string{"low", "high"}},
	}
	if err := catalog.SaveLive(accountModels("codex", user), CodexBase, models); err != nil {
		t.Fatal(err)
	}
	model, effort, err := warmModel(user)
	if err != nil || model != "gpt-6.1-sol" || effort != "low" {
		t.Fatalf("warm-up picked %q at %q: %v; want the allowed model", model, effort, err)
	}
	if err := catalog.SaveLive(accountModels("codex", user), CodexBase, models[:1]); err != nil {
		t.Fatal(err)
	}
	if model, _, err := warmModel(user); err == nil || model != "" {
		t.Fatalf("warm-up with no allowed model picked %q: %v", model, err)
	}
}

// Direct warm-up calls and picks scheduled before a policy change still
// pass the final check immediately before sending an HTTP request.
func TestCodexWarmRequestModelPolicy(t *testing.T) {
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
			t.Errorf("upstream received forbidden model %q", body.Model)
		}
		io.WriteString(w, `{"id":"warm-ok"}`)
	}))
	t.Cleanup(fake.Close)
	was := CodexBase
	CodexBase = fake.URL
	t.Cleanup(func() { CodexBase = was })
	sign := func(context.Context, *http.Request, []byte) error { return nil }
	if err := warmCodex(t.Context(), sign, "gpt-6.1-sol", "low"); err != nil {
		t.Fatal(err)
	}
	if err := warmCodex(t.Context(), sign, "gpt-6-luna", "low"); err == nil {
		t.Error("a direct warm-up bypassed the model policy")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream received %d requests, want only the allowed one", got)
	}
	change := func(context.Context, *http.Request, []byte) error {
		s := settings.Load()
		s.AllowedModels = []string{"gpt-6-sol"}
		return settings.Save(s)
	}
	if err := warmCodex(t.Context(), change, "gpt-6.1-sol", "low"); err == nil {
		t.Error("a stale warm-up pick bypassed the changed policy")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("the stale pick reached upstream: %d requests", got)
	}
}
