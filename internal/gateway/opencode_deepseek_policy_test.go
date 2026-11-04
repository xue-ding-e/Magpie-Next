package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// The DS instance has an exact model policy and no sign-ins or fallbacks.
// A Codex desktop request uses the same backend path as its existing Sol
// chats; only the explicit provider/model is sent to the DS upstream.
func TestOpenCodeDeepSeekCodexBackendAndIsolatedPolicy(t *testing.T) {
	s, captured := dsProfileServer(t, chatSSE("DS_CODEX_OK", "", ""))
	set := settings.Load()
	set.AllowedModels = []string{dsCapturedModel}
	set.NoAccountDiscovery = true
	if err := settings.Save(set); err != nil {
		t.Fatal(err)
	}
	send := func(model string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, CodexPath+"/responses", strings.NewReader(`{"model":"`+model+`","input":"hello","stream":true,"reasoning":{"effort":"max"}}`))
		req.Header.Set("User-Agent", "Codex Desktop/0.162.3")
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := send("ds/" + dsCapturedModel); rec.Code != 200 || !strings.Contains(rec.Body.String(), "DS_CODEX_OK") {
		t.Fatalf("explicit DS: %d %s", rec.Code, rec.Body)
	}
	for _, model := range []string{"gpt-6.1-sol", "ds/gpt-6.1-sol", "other/gpt-6.1-sol", "group/ds"} {
		if rec := send(model); rec.Code == http.StatusOK {
			t.Errorf("model %q escaped the DS-only policy: %s", model, rec.Body)
		}
	}
	got := captured()
	if len(got) != 1 || got[0].body["model"] != dsCapturedModel {
		t.Fatalf("only the explicit DS request may go upstream: %+v", got)
	}
}
