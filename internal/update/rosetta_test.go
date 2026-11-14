package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// onSystem runs the test as a magpie built for sys/arch, translated or
// not.
func onSystem(t *testing.T, sys, arch string, tr bool) {
	t.Helper()
	wasOS, wasArch, wasTr := goos, goarch, translated
	t.Cleanup(func() { goos, goarch, translated = wasOS, wasArch, wasTr })
	goos, goarch, translated = sys, arch, func() bool { return tr }
}

// Upstream bundle selection retains Rosetta's Apple silicon architecture.
// Next's bare binaries keep the fixed asset names published by this fork,
// whatever platform the inherited bundle fixtures simulate.
func TestRosettaUpdatesToAppleSilicon(t *testing.T) {
	was := GUI
	t.Cleanup(func() { GUI = was })
	for _, c := range []struct {
		os, arch string
		tr, gui  bool
		app, bin string
	}{
		{"darwin", "amd64", true, true, "magpie-darwin-arm64.zip", "Magpie-Next.exe"},
		{"darwin", "amd64", true, false, "magpie-darwin-arm64.zip", "magpie-next-cli.exe"},
		{"darwin", "amd64", false, true, "magpie-darwin-amd64.zip", "Magpie-Next.exe"},
		{"darwin", "arm64", false, true, "magpie-darwin-arm64.zip", "Magpie-Next.exe"},
		// only macOS has Rosetta: a translated answer elsewhere changes nothing
		{"windows", "amd64", true, true, "magpie-darwin-amd64.zip", "Magpie-Next.exe"},
		{"linux", "amd64", true, false, "magpie-darwin-amd64.zip", "magpie-next-cli.exe"},
		{"linux", "arm64", false, true, "magpie-darwin-arm64.zip", "Magpie-Next.exe"},
	} {
		onSystem(t, c.os, c.arch, c.tr)
		GUI = c.gui
		if got := AppAsset(); got != c.app {
			t.Errorf("%s/%s translated=%v: AppAsset = %s, want %s", c.os, c.arch, c.tr, got, c.app)
		}
		if got := BinaryAsset(); got != c.bin {
			t.Errorf("%s/%s translated=%v gui=%v: BinaryAsset = %s, want %s", c.os, c.arch, c.tr, c.gui, got, c.bin)
		}
	}
}

// Under Rosetta the update downloads Apple silicon's zip, not Intel's,
// and swaps it in for the Intel app as any update is: signed by the same
// team, renamed over the bundle.
func TestRosettaStagesAppleSiliconsApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ditto and codesign")
	}
	stubUnregister(t)
	onSystem(t, "darwin", "amd64", true)
	root := t.TempDir()
	zips := map[string][]byte{}
	assets := map[string]Asset{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zips[strings.TrimPrefix(r.URL.Path, "/")])
	}))
	defer srv.Close()
	for _, a := range []string{"amd64", "arm64"} {
		src := filepath.Join(root, "src-"+a)
		fakeApp(t, filepath.Join(src, "magpie.app"), "2.0-"+a)
		name := "magpie-darwin-" + a + ".zip"
		zip := filepath.Join(root, name)
		cmd := exec.Command("ditto", "-c", "-k", "--keepParent", "magpie.app", zip)
		cmd.Dir = src
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ditto: %v: %s", err, b)
		}
		body, err := os.ReadFile(zip)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		zips[name] = body
		assets[name] = Asset{URL: srv.URL + "/" + name, SHA256: hex.EncodeToString(sum[:])}
	}
	bundle := filepath.Join(root, "Applications", "magpie.app")
	fakeApp(t, bundle, "1.0-amd64")
	staged, err := Stage(context.Background(), &Release{Version: "2.0", Assets: assets}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(staged, bundle); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if !strings.Contains(string(b), "<string>2.0-arm64</string>") {
		t.Errorf("Apple silicon's app isn't in place: %s", b)
	}
}
