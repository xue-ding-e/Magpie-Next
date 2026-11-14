package cli

import (
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

// magpie accounts export (heyiwei5 and Rk1ngggg on X): warns first, writes
// nothing until agreed to, writes 0600 files that import back, never prints
// a token and never writes over a file.
func TestAccountsExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		who := strings.TrimPrefix(body["refresh_token"], "r-")
		json.NewEncoder(w).Encode(map[string]any{
			"id_token": importJWT(map[string]any{"email": who + "@example.com",
				"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "pro", "chatgpt_account_id": "acct-" + who}}),
			"access_token":  importJWT(map[string]any{"exp": float64(time.Now().Add(time.Hour).Unix())}),
			"refresh_token": "n-" + who,
		})
	}))
	defer fake.Close()
	t.Cleanup(provider.CodexTokenURLForTest(fake.URL))
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	oldIn := os.Stdin
	os.Stdin = null
	t.Cleanup(func() { os.Stdin = oldIn })

	src := filepath.Join(t.TempDir(), "a.json")
	// This fixture needs renewal; a usable snapshot is checked without
	// spending its refresh token (TestAccountsAuthSnapshotExport).
	os.WriteFile(src, []byte(`{"OPENAI_API_KEY":null,"tokens":{"id_token":"x","access_token":"","refresh_token":"r-a","account_id":"acct-a"}}`), 0o600)
	if out, err := said(t, func() error { return accountsCmd([]string{"accounts", "import", "codex", src, "--yes"}) }); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	held := provider.Logins("codex")
	if len(held) != 1 {
		t.Fatalf("logins %+v", held)
	}

	out := t.TempDir()
	run := func(args ...string) (string, error) {
		t.Helper()
		return said(t, func() error { return accountsCmd(append([]string{"accounts", "export"}, args...)) })
	}
	// not agreed to: the warning, no file
	says, err := run("codex", "--to", out)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("exported without --yes: %v\n%s", err, says)
	}
	if es, _ := os.ReadDir(out); len(es) != 0 {
		t.Fatalf("wrote before agreed: %v", es)
	}
	for _, format := range []string{"sub2api", "cliproxyapi"} {
		says, err = run("codex", "--format", format, "--to", out, "--yes")
		if err != nil {
			t.Fatalf("%s: %v\n%s", format, err, says)
		}
		if strings.Contains(says, "n-a") || strings.Contains(says, "r-a") {
			t.Errorf("a token was printed:\n%s", says)
		}
	}
	es, _ := os.ReadDir(out)
	if len(es) != 2 {
		t.Fatalf("files %v", es)
	}
	for _, e := range es {
		p := filepath.Join(out, e.Name())
		if runtime.GOOS != "windows" {
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s is %v, not 0600", e.Name(), fi.Mode().Perm())
			}
		}
	}
	// again: the first files stay, new ones are named beside them
	if _, err = run("codex", "--format", "sub2api", "--to", out, "--yes"); err != nil {
		t.Fatal(err)
	}
	if es, _ := os.ReadDir(out); len(es) != 3 {
		t.Errorf("an export wrote over a file: %v", es)
	}
	if _, err := run("codex", "--format", "csv", "--to", out, "--yes"); err == nil {
		t.Error("an unknown format was taken")
	}
}

func TestAccountsAuthSnapshotExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	var verified, refreshed atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshed.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"refresh_token_reused"}`))
			return
		}
		verified.Add(1)
		if r.URL.Path != "/wham/usage" || r.Header.Get("Authorization") != "Bearer synthetic-access" || r.Header.Get("chatgpt-account-id") != "synthetic-selected-seat" {
			t.Error("the imported token or selected workspace was changed")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"plus"}`))
	}))
	t.Cleanup(fake.Close)
	t.Cleanup(provider.CodexTokenURLForTest(fake.URL + "/token"))
	oldBase := provider.CodexBase
	provider.CodexBase = fake.URL + "/codex"
	t.Cleanup(func() { provider.CodexBase = oldBase })
	id := importJWT(map[string]any{"email": "snapshot@example.invalid", "https://api.openai.com/auth": map[string]any{
		"chatgpt_account_id": "synthetic-default-seat", "chatgpt_plan_type": "plus"}})
	content, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{
		"id_token": id, "access_token": "synthetic-access", "refresh_token": "synthetic-used-refresh", "account_id": "synthetic-selected-seat"},
		"last_refresh": "2026-01-01T00:00:00Z", "future": json.RawMessage(`{"number":9007199254740993,"exponent":1e999}`)})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	says, err := said(t, func() error { return accountsCmd([]string{"accounts", "import", "codex", src, "--yes"}) })
	if err != nil || !strings.Contains(says, "verified first") || !strings.Contains(says, "added snapshot@example.invalid") {
		t.Fatalf("usable snapshot import failed: %v\n%s", err, says)
	}
	dir := t.TempDir()
	says, err = said(t, func() error {
		return accountsCmd([]string{"accounts", "export", "codex", "--format", "cliproxyapi", "--to", dir, "--yes"})
	})
	if err != nil || strings.Contains(says, "synthetic-access") || strings.Contains(says, "synthetic-used-refresh") {
		t.Fatal("export failed or printed credentials", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("export did not write one account file", err)
	}
	exported, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil || !strings.Contains(string(exported), "synthetic-used-refresh") || !strings.Contains(string(exported), "synthetic-selected-seat") {
		t.Fatal("export lost the source's token or workspace", err)
	}
	_, revision, err := provider.CodexAuthJSON("snapshot@example.invalid", "", false)
	if err != nil {
		t.Fatal(err)
	}
	kept, _, err := provider.CodexAuthJSON("snapshot@example.invalid", revision, true)
	if err != nil || !strings.Contains(string(kept), "9007199254740993") || !strings.Contains(string(kept), "1e999") {
		t.Fatal("CLI import lost precise native auth.json metadata", err)
	}
	stillSource, _ := os.ReadFile(src)
	if string(stillSource) != string(content) || verified.Load() != 1 || refreshed.Load() != 0 {
		t.Fatal("the source file changed or its refresh token was spent")
	}
}
