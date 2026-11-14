package gui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// POST /api/signin/export (heyiwei5 and Rk1ngggg on X): the accounts are
// written 0600 into Downloads, in a folder of their own for CLIProxyAPI,
// and the answer names the file and never holds a token.
func TestSigninExportWritesPrivateFiles(t *testing.T) {
	h := sandboxHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(h, ".codex"))
	if err := os.MkdirAll(filepath.Join(h, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	claims := func(m map[string]any) string {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
	}
	access := claims(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	var verified, refreshed atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshed.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/wham/usage" || r.Header.Get("Authorization") != "Bearer "+access || r.Header.Get("chatgpt-account-id") != "acct-a" {
			t.Errorf("unexpected snapshot verification: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		verified.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"plan_type": "pro"})
	}))
	t.Cleanup(fake.Close)
	t.Cleanup(provider.CodexTokenURLForTest(fake.URL + "/token"))
	oldBase := provider.CodexBase
	provider.CodexBase = fake.URL + "/codex"
	t.Cleanup(func() { provider.CodexBase = oldBase })
	// A complete snapshot imports through its usable access token; exporting
	// it must preserve the source tool's refresh token without consuming it.
	seed, err := json.Marshal(map[string]any{"tokens": map[string]any{
		"id_token": claims(map[string]any{"email": "a@example.com",
			"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "pro", "chatgpt_account_id": "acct-a"}}),
		"access_token": access, "refresh_token": "SECRET-refresh", "account_id": "acct-a",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := provider.ImportLogins(context.Background(), "codex", []string{string(seed)}); err != nil || len(res) != 1 || res[0].Status != "added" {
		t.Fatalf("%+v %v", res, err)
	}
	mux := http.NewServeMux()
	providerRoutes(mux, folderOnly{})
	post := func(body any) (int, string, map[string]any) {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/signin/export", strings.NewReader(string(b))))
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, w.Body.String(), out
	}
	for _, format := range []string{"sub2api", "cliproxyapi"} {
		code, raw, out := post(map[string]any{"agent": "codex", "format": format})
		if code != 200 {
			t.Fatalf("%s: %d %s", format, code, raw)
		}
		if strings.Contains(raw, "SECRET") {
			t.Errorf("%s: a token is in the answer: %s", format, raw)
		}
		p, _ := out["path"].(string)
		if !strings.HasPrefix(p, "~") {
			t.Fatalf("%s: path %q", format, p)
		}
		file := filepath.Join(h, strings.TrimPrefix(p, "~"))
		if format == "cliproxyapi" && !strings.Contains(file, "magpie-cliproxyapi-codex-") {
			t.Errorf("not in a folder of its own: %s", file)
		}
		data, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(data), "SECRET-refresh") {
			t.Fatalf("%s: %v %s", format, err, data)
		}
		if fi, _ := os.Stat(file); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v, not 0600", file, fi.Mode().Perm())
		}
	}
	if code, _, _ := post(map[string]any{"agent": "codex", "format": "sub2api", "users": []string{"nobody@example.com"}}); code == 200 {
		t.Error("an account that isn't there was exported")
	}
	if code, _, _ := post(map[string]any{"agent": "gemini", "format": "sub2api"}); code == 200 {
		t.Error("gemini was exported")
	}
	if verified.Load() != 1 || refreshed.Load() != 0 {
		t.Fatalf("snapshot verification = %d, refreshes = %d; want 1 and 0", verified.Load(), refreshed.Load())
	}
}
