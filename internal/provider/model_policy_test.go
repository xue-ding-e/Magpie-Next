package provider

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/testenv"
)

func modelPolicyHome(t *testing.T, models []string) {
	t.Helper()
	dir := t.TempDir()
	testenv.SetHome(t, dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
	var s settings.Settings
	s.AllowedModels = models
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
}

// Only the exact id sent upstream passes; a provider prefix, another
// version, a hidden reviewer and a differently cased id do not.
func TestRequireAllowedModelExact(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol", "vendor/model"})
	for _, model := range []string{"gpt-6.1-sol", "vendor/model"} {
		if err := RequireAllowedModel(model); err != nil {
			t.Errorf("allowed %q: %v", model, err)
		}
	}
	for _, model := range []string{"gpt-6-sol", "codex/gpt-6.1-sol", "codex-auto-review", "GPT-6.1-SOL", "gpt-6.1-sol-mini", "model", ""} {
		if err := RequireAllowedModel(model); err == nil {
			t.Errorf("forbidden model %q was allowed", model)
		}
	}
}

func TestRequireAllowedModelUnset(t *testing.T) {
	modelPolicyHome(t, nil)
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-sol", "codex-auto-review", "vendor/model"} {
		if err := RequireAllowedModel(model); err != nil {
			t.Errorf("unset policy blocked %q: %v", model, err)
		}
	}
}

func TestRequireAllowedModelInvalidSavedPolicy(t *testing.T) {
	for _, policy := range []string{
		`{"allowedModels":["gpt-6.1-sol","*"]}`,
		`{"allowedModels":["gpt-6.1-sol",""]}`,
		`{"allowedModels":["gpt-6.1-sol","bad\nid"]}`,
	} {
		t.Run(policy, func(t *testing.T) {
			modelPolicyHome(t, nil)
			if err := os.WriteFile(settings.Path(), []byte(policy), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RequireAllowedModel("gpt-6.1-sol"); err == nil {
				t.Fatal("an invalid hand-edited policy allowed a request")
			}
		})
	}
}

// Updating the allowlist takes effect without restarting the process.
func TestRequireAllowedModelChanged(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	if err := RequireAllowedModel("gpt-6-sol"); err == nil {
		t.Fatal("the initial policy allowed another model")
	}
	s := settings.Load()
	s.AllowedModels = []string{"gpt-6-sol"}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if err := RequireAllowedModel("gpt-6-sol"); err != nil {
		t.Fatal(err)
	}
	if err := RequireAllowedModel("gpt-6.1-sol"); err == nil {
		t.Fatal("the old model stayed allowed")
	}
}

// A failed settings read is unknown even when the last request read an
// unrestricted list; the refusal never repeats settings or credentials.
func TestRequireAllowedModelUnreadSettings(t *testing.T) {
	for _, mode := range []string{"broken JSON", "unreadable path"} {
		t.Run(mode, func(t *testing.T) {
			modelPolicyHome(t, nil)
			if err := RequireAllowedModel("gpt-6-sol"); err != nil {
				t.Fatal(err)
			}
			path := settings.Path()
			if err := os.Remove(path + ".bak"); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			const secret = "must-not-repeat-this-credential"
			if mode == "broken JSON" {
				if err := os.WriteFile(path, []byte(`{"lanKey":"`+secret+`",`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := RequireAllowedModel("gpt-6.1-sol"); err == nil {
				t.Fatal("unknown policy was treated as unrestricted")
			} else if !strings.Contains(err.Error(), "settings.json") || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), filepath.Dir(path)) {
				t.Fatalf("refusal should name settings.json without its contents or path: %v", err)
			}
		})
	}
}

func TestRequireAllowedModelConcurrentReads(t *testing.T) {
	modelPolicyHome(t, []string{"gpt-6.1-sol"})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 20 {
				if err := RequireAllowedModel("gpt-6.1-sol"); err != nil {
					t.Error(err)
					return
				}
				if err := RequireAllowedModel("gpt-6-sol"); err == nil {
					t.Error("a concurrent read allowed the forbidden model")
					return
				}
			}
		})
	}
	wg.Wait()
}
