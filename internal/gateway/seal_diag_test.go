package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestRoutedCodexRefusalIsDiagnosedBeforeNormalization(t *testing.T) {
	fresh(t)
	path := filepath.Join(t.TempDir(), "routed-refusal.json")
	t.Setenv("MAGPIE_SEAL_DIAG", path)
	t.Setenv("MAGPIE_NO_ACCOUNTS", "")
	const refusal = `{"detail":[{"loc":["body","input",1],"msg":"synthetic-rejection"}]}`
	const input = `{"model":"m","input":[{"type":"reasoning","encrypted_content":"synthetic-seal-not-written"},{"role":"user","content":"synthetic-prompt-not-written"}]}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, refusal)
	}))
	t.Cleanup(up.Close)
	s := New()
	p := provider.Provider{ID: "codex-fixture", Name: "Codex fixture", Key: "synthetic-key", Responses: up.URL,
		Account: &provider.Account{Agent: "codex"}}
	res, err := s.forward(context.Background(), p, provider.Responses, "/responses", []byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	got, err := io.ReadAll(res.Body)
	if err != nil || string(got) != refusal || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("diagnostics changed the reply: %d %q %v", res.StatusCode, got, err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("routed refusal was not recorded: %v", err)
	}
	var rec struct {
		Status int
		Body   string
		Sealed map[string]map[string]int
	}
	if err := json.Unmarshal(stored, &rec); err != nil || rec.Status != http.StatusBadRequest || rec.Body != refusal || rec.Sealed["reasoning"]["sealed"] != 1 {
		t.Fatalf("raw routed refusal missing: %+v %v", rec, err)
	}
	if strings.Contains(string(stored), "synthetic-seal-not-written") || strings.Contains(string(stored), "synthetic-prompt-not-written") {
		t.Fatal("routed diagnostics copied request contents")
	}
}

func TestSealDiagnosticsAppendOnlyMetadataAndRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refusals.json")
	t.Setenv("MAGPIE_SEAL_DIAG", path)
	const input = `{"input":[{"type":"reasoning","encrypted_content":"synthetic-seal-not-written"},{"type":"message","content":"synthetic-prompt-not-written"}]}`
	diagSealRefusal(400, http.Header{"Content-Type": {"application/json"}}, []byte("synthetic-refusal"), []byte(input))
	diagTurnRefusal("turn", "/responses", 400, []byte("synthetic-refusal"), []byte(input))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "synthetic-seal-not-written") || strings.Contains(string(b), "synthetic-prompt-not-written") {
		t.Fatal("diagnostics copied request contents")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	for range 2 {
		var rec struct {
			Status int
			Body   string
			Sealed map[string]map[string]int
		}
		if err := d.Decode(&rec); err != nil {
			t.Fatal(err)
		}
		if rec.Status != 400 || rec.Body != "synthetic-refusal" || rec.Sealed["reasoning"]["sealed"] != 1 || rec.Sealed["message"]["plain"] != 1 {
			t.Fatal("diagnostics lost refusal or shape metadata")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("diagnostics file is not private")
	}
}

func TestSealDiagnosticsIgnoreRelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "relative.json")
	t.Setenv("MAGPIE_SEAL_DIAG", "relative.json")
	diagSealRefusal(400, nil, []byte("synthetic-refusal"), nil)
	diagTurnRefusal("turn", "/responses", 400, []byte("synthetic-refusal"), nil)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("relative output path was written", err)
	}
}

func TestSealDiagnosticsRefuseSharedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not describe Windows ACLs")
	}
	path := filepath.Join(t.TempDir(), "shared.json")
	const original = "synthetic-existing-data"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_SEAL_DIAG", path)
	diagSealRefusal(400, nil, []byte("synthetic-refusal"), nil)
	diagTurnRefusal("turn", "/responses", 400, []byte("synthetic-refusal"), nil)
	b, err := os.ReadFile(path)
	if err != nil || string(b) != original {
		t.Fatal("diagnostics appended to a file readable by other users")
	}
}

func TestSealDiagnosticsRefuseSymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target.json"), filepath.Join(dir, "link.json")
	const original = "synthetic-existing-data"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("MAGPIE_SEAL_DIAG", link)
	diagSealRefusal(400, nil, []byte("synthetic-refusal"), nil)
	diagTurnRefusal("turn", "/responses", 400, []byte("synthetic-refusal"), nil)
	b, err := os.ReadFile(target)
	if err != nil || string(b) != original {
		t.Fatal("diagnostics followed a symlink")
	}
}
