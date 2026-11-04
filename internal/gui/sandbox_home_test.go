package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agentenv"
)

func TestSandboxHomePlatformDirectories(t *testing.T) {
	inherited := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(inherited, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(inherited, "Local"))
	for _, name := range agentenv.Vars {
		t.Setenv(name, "synthetic-inherited-override")
	}

	home := sandboxHome(t)
	for name, want := range map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
	} {
		if got := os.Getenv(name); got != want {
			t.Fatalf("%s is not the sandbox's private directory", name)
		}
	}
	if os.Getenv("PATH") != "" {
		t.Fatal("sandbox retained executable discovery paths")
	}
	for _, name := range agentenv.Vars {
		if os.Getenv(name) != "" {
			t.Fatalf("sandbox retained agent override %s", name)
		}
	}
	for name, lookup := range map[string]func() (string, error){
		"config": os.UserConfigDir,
		"cache":  os.UserCacheDir,
	} {
		dir, err := lookup()
		if err != nil {
			t.Fatalf("sandbox %s lookup: %v", name, err)
		}
		rel, err := filepath.Rel(home, dir)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("sandbox %s lookup escaped its private home", name)
		}
	}
}
