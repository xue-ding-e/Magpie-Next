//go:build windows && !nogui

package gui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// This probes the real host.Copy adapter and Windows clipboard, not the UI
// route or JS modal. Those remain separate end-to-end acceptance requirements.
func TestCodexAuthWindowsClipboardAdapterProbe(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("RUNNER_OS") != "Windows" || os.Getenv("MAGPIE_AUTH_ALLOW_DISPOSABLE_CLIPBOARD") != "1" {
		t.Fatal("disposable CI clipboard probe not explicitly enabled")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".synthetic-native-probe"), []byte("synthetic-only-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(os.Environ(), "MAGPIE_AUTH_CLIPBOARD_CHILD="+root, "HOME="+root, "USERPROFILE="+root, "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"), "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_STATE_HOME="+filepath.Join(root, "state"), "APPDATA="+filepath.Join(root, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(root, "AppData", "Local"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "AUTH_NATIVE_CLIPBOARD_ADAPTER_OK") {
		t.Fatalf("native adapter probe: %v\n%s", err, out)
	}
}

func init() {
	root := os.Getenv("MAGPIE_AUTH_CLIPBOARD_CHILD")
	if root == "" {
		return
	}
	marker, err := os.ReadFile(filepath.Join(root, ".synthetic-native-probe"))
	if err != nil || string(marker) != "synthetic-only-v1" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("RUNNER_OS") != "Windows" || os.Getenv("MAGPIE_AUTH_ALLOW_DISPOSABLE_CLIPBOARD") != "1" {
		os.Exit(80)
	}
	app := application.New(application.Options{Name: "Synthetic auth clipboard adapter probe"})
	h := &host{app: app}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			passed := false
			ownedText := ""
			defer func() {
				cleared := false
				if ownedText != "" {
					current, readable := app.Clipboard.Text()
					if readable && current == ownedText {
						cleared = h.Copy("synthetic-probe-cleared")
					} else {
						// Never log an unexpected clipboard value or overwrite it.
						fmt.Println("AUTH_NATIVE_CLIPBOARD_OWNERSHIP_LOST")
					}
				}
				if passed && cleared {
					fmt.Println("AUTH_NATIVE_CLIPBOARD_ADAPTER_OK")
				}
				if !cleared {
					fmt.Println("AUTH_NATIVE_CLIPBOARD_CLEANUP_FAILED")
				}
				app.Quit()
			}()
			// Read only after overwriting this disposable session with known fake text.
			// Never capture or restore a pre-existing clipboard value.
			for _, text := range []string{"synthetic-clipboard-sentinel", `{"synthetic":"account-A"}`, `{"synthetic":"account-B"}`} {
				if !h.Copy(text) {
					fmt.Println("AUTH_NATIVE_CLIPBOARD_WRITE_FAILED")
					return
				}
				ownedText = text
				got, ok := app.Clipboard.Text()
				if !ok || got != text {
					fmt.Println("AUTH_NATIVE_CLIPBOARD_READBACK_FAILED")
					return
				}
			}
			passed = true
		}()
	})
	if err := app.Run(); err != nil {
		fmt.Println("AUTH_NATIVE_CLIPBOARD_APPLICATION_FAILED")
		os.Exit(81)
	}
	os.Exit(0)
}
