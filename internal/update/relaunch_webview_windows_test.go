package update

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/webview2env"
)

func TestRelaunchKeepsInitialWebViewArguments(t *testing.T) {
	initial := webview2env.Args
	t.Cleanup(func() { webview2env.Args = initial })
	webview2env.Args = `--disable-features=RendererCodeIntegrity --remote-debugging-port=9444 --user-agent="synthetic QA"`
	// Wails beta.24's loader blanks this variable after webview2env captured it.
	t.Setenv(webview2env.Name, "")
	for _, window := range []bool{true, false} {
		for _, checked := range []bool{false, true} {
			t.Run(strconv.FormatBool(window)+"/checked="+strconv.FormatBool(checked), func(t *testing.T) {
				exe, old := filepath.Join(t.TempDir(), "synthetic.exe"), ""
				if checked {
					old = exe + ".old"
				}
				stubLaunch(t, func(_ string, _ []string, env []string) (<-chan struct{}, func(), error) {
					values := map[string]string{}
					for _, entry := range env {
						name, value, _ := strings.Cut(entry, "=")
						values[strings.ToUpper(name)] = value
					}
					if got := values[webview2env.Name]; got != webview2env.Args {
						t.Errorf("relaunched browser arguments = %q, want the initial arguments %q", got, webview2env.Args)
					}
					if got := values["MAGPIE_REPLACES"]; got != strconv.Itoa(os.Getpid()) {
						t.Errorf("predecessor = %q", got)
					}
					if marker := values[startedEnv]; marker != "" {
						if err := os.WriteFile(marker, []byte("synthetic"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					return nil, func() {}, nil
				})
				if err := RelaunchBinary(exe, old, window, "settings"); err != nil {
					t.Fatal(err)
				}
				if got := os.Getenv(webview2env.Name); got != "" {
					t.Fatal("relaunch changed the current process's cleared loader environment")
				}
			})
		}
	}
}

func TestRelaunchWithoutInitialWebViewArguments(t *testing.T) {
	initial := webview2env.Args
	t.Cleanup(func() { webview2env.Args = initial })
	webview2env.Args = ""
	t.Setenv(webview2env.Name, "synthetic later value")
	stubLaunch(t, func(_ string, _ []string, env []string) (<-chan struct{}, func(), error) {
		var got string
		for _, entry := range env {
			name, value, _ := strings.Cut(entry, "=")
			if strings.EqualFold(name, webview2env.Name) {
				got = value
			}
		}
		if got != "synthetic later value" {
			t.Errorf("relaunch replaced an uncaptured value with %q", got)
		}
		return nil, func() {}, nil
	})
	if err := RelaunchBinary("synthetic.exe", "", true, "settings"); err != nil {
		t.Fatal(err)
	}
}
