package gui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/steady"
)

func TestCodexAuthExportAtomicFailureAndMode(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(original, []byte("leave-existing-alone"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldSync := steady.Sync
	t.Cleanup(func() { steady.Sync = oldSync })
	steady.Sync = func(*os.File) error { return errors.New("synthetic flush failure") }
	if p, err := saveCodexAuthExport(context.Background(), dir, []byte(`{"synthetic":true}`)); err == nil || p != "" {
		t.Fatal("failed flush reported success")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("failed export left a partial file or directory")
	}
	steady.Sync = oldSync
	p, err := saveCodexAuthExport(context.Background(), dir, []byte(`{"synthetic":true}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != `{"synthetic":true}` {
		t.Fatal("export did not persist exact bytes")
	}
	b, _ = os.ReadFile(original)
	if string(b) != "leave-existing-alone" {
		t.Fatal("export overwrote existing auth.json")
	}
	// Unix mode bits are not an ACL guarantee on Windows.
	if runtime.GOOS != "windows" {
		f, _ := os.Stat(p)
		d, _ := os.Stat(filepath.Dir(p))
		if f.Mode().Perm() != 0o600 || d.Mode().Perm() != 0o700 {
			t.Fatal("export is not owner-only")
		}
	}
}

func TestCodexAuthExportWebAuthenticationAndPreview(t *testing.T) {
	sandboxHome(t)
	path := filepath.Join(filepath.Dir(provider.Path()), "logins.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := `[{"id":"synthetic-id","agent":"codex","user":"synthetic@example.invalid","auth":{"auth_mode":"chatgpt","tokens":{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","id_token":"synthetic-id-token","account_id":"synthetic-account"},"future":{"secret":"synthetic-future"}}}]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	codexAuthRoutes(mux, webHost{})
	// This is the production web-cookie guard, not a fake authorization
	// header. A rebound hostname without the run key must still fail.
	h := webGuard("synthetic-cookie", "synthetic-run-key", 0, nil, webOwnPage(noFramedAPI(mux)))
	post := func(action, identity, origin, host, contentType string, cookie bool) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"user": "synthetic@example.invalid", "revision": identity, "action": action})
		r := httptest.NewRequest("POST", "http://"+host+"/api/login/auth-json", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", contentType)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: "synthetic-cookie", Value: "synthetic-run-key"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, origin := range []string{"", "http://rebound.invalid"} {
		if w := post("reveal", "synthetic-id", origin, "rebound.invalid", "application/json", false); w.Code != http.StatusUnauthorized {
			t.Fatal("rebound request without run key was accepted")
		}
	}
	for _, origin := range []string{"null", "http://host.invalid:9999", "http://evil.invalid", "file://host.invalid"} {
		if w := post("reveal", "synthetic-id", origin, "host.invalid", "application/json", true); w.Code != http.StatusForbidden {
			t.Fatalf("foreign origin accepted: %q", origin)
		}
	}
	if w := post("export", "synthetic-id", "", "host.invalid", "text/plain", true); w.Code != http.StatusForbidden {
		t.Fatal("simple cross-site form was accepted")
	}
	w := post("preview", "", "http://host.invalid", "host.invalid", "application/json", true)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "synthetic-access") || strings.Contains(w.Body.String(), "synthetic-future") {
		t.Fatal("default preview exposed credentials or could be cached")
	}
	var preview struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || preview.Revision == "" {
		t.Fatal("preview has no revision")
	}
	w = post("reveal", preview.Revision, "http://host.invalid", "host.invalid", "application/json", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "synthetic-access") {
		t.Fatal("explicit authenticated reveal did not preserve the saved snapshot")
	}
	// Same user and persistent login ID, different workspace and token.
	replacement := strings.ReplaceAll(strings.ReplaceAll(fixture, "synthetic-account", "synthetic-workspace-b"), "synthetic-access", "synthetic-access-b")
	if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"reveal", "export"} {
		w = post(action, preview.Revision, "http://host.invalid", "host.invalid", "application/json", true)
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "synthetic-access-b") {
			t.Fatal("stale preview exported replacement snapshot")
		}
	}
	// A broken current store must not export a cached last-good generation.
	if err := os.WriteFile(path, []byte("broken synthetic JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	w = post("reveal", preview.Revision, "http://host.invalid", "host.invalid", "application/json", true)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "synthetic-access") || strings.Contains(w.Body.String(), "broken synthetic JSON") {
		t.Fatal("failed read exported cached secrets or echoed raw data")
	}
}

func TestCodexAuthExportCancelDuringFlush(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldSync := steady.Sync
	t.Cleanup(func() { steady.Sync = oldSync })
	steady.Sync = func(f *os.File) error { cancel(); return oldSync(f) }
	if p, err := saveCodexAuthExport(ctx, dir, []byte(`{"synthetic":true}`)); err == nil || p != "" {
		t.Fatal("cancelled export reported success")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled export left credentials on disk")
	}
}

func TestCodexAuthExportRequiresTrustedTransport(t *testing.T) {
	for _, origin := range []string{"", "http://rebound.invalid", "wails://rebound.invalid"} {
		r := httptest.NewRequest("POST", "http://rebound.invalid/api/login/auth-json", nil)
		if codexAuthOrigin(r, origin) {
			t.Fatal("untrusted TCP request accepted")
		}
	}
	for _, scheme := range []string{"wails", "http"} {
		r := httptest.NewRequest("POST", "http://wails.localhost/api/login/auth-json", nil)
		r = r.WithContext(context.WithValue(r.Context(), windowSchemeKey{}, scheme))
		if !codexAuthOrigin(r, scheme+"://wails.localhost") || !codexAuthOrigin(r, "") {
			t.Fatal("native transport rejected")
		}
		if codexAuthOrigin(r, "null") || codexAuthOrigin(r, scheme+"://other.localhost") {
			t.Fatal("foreign native origin accepted")
		}
	}
}
