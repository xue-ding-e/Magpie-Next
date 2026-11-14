package settings

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filememo"
)

// The gateway's allow list asks SharedChecked on every request: an
// unchanged settings.json gives its settings without a copy, a change is
// read at once, and a file that doesn't parse is unknown (an error), as
// LoadChecked says, never an empty list.
func TestSharedCheckedReadsOnceAndKnowsUnknown(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var images []string
	for i := range 60 {
		images = append(images, fmt.Sprintf(`"p/m%d":true`, i))
	}
	writeRaw(t, `{"allowIPs":["10.0.0.0/8","192.168.1.5"],"redactWords":["a","b"],"pickedModels":{"codex":["p/m1"]},"modelImages":{`+strings.Join(images, ",")+`}}`)
	s, err := SharedChecked()
	if err != nil || len(s.AllowIPs) != 2 || s.AllowIPs[0] != "10.0.0.0/8" {
		t.Fatalf("read %v, %v", s.AllowIPs, err)
	}
	for _, age := range []string{"recent", "settled"} {
		t.Run(age, func(t *testing.T) {
			if age == "settled" {
				at := time.Now().Add(-time.Hour)
				if err := os.Chtimes(Path(), at, at); err != nil {
					t.Fatal(err)
				}
			}
			// The path and file check allocate on Windows; a recent file
			// also compares its bytes. Cost those on the same file, with no
			// Settings read, so the bounds measure copying or rebuilding it.
			checked := testing.AllocsPerRun(50, func() {
				filememo.Read("settings", pathNow(), func(b []byte) ([]byte, error) { return b, nil })
			})
			n := Parses()
			shared := testing.AllocsPerRun(50, func() { SharedChecked() })
			copied := testing.AllocsPerRun(50, func() { LoadChecked() })
			t.Logf("allocations/read: file check %.0f, shared %.0f, copied %.0f; additional parses %d", checked, shared, copied, Parses()-n)
			if shared-checked > 6 || (shared-checked)*4 > copied-checked {
				t.Errorf("an unchanged file: %.0f allocations a read above its file check (LoadChecked's copy: %.0f), want no copy", shared-checked, copied-checked)
			}
			if got := Parses() - n; got != 0 {
				t.Errorf("an unchanged file was parsed %d more times", got)
			}
		})
	}
	writeRaw(t, `{"allowIPs":["127.0.0.1"]}`)
	if s, err := SharedChecked(); err != nil || len(s.AllowIPs) != 1 || s.AllowIPs[0] != "127.0.0.1" {
		t.Fatalf("a change: %v, %v", s.AllowIPs, err)
	}
	writeRaw(t, `{"allowIPs":["127.0.0.1"`)
	if _, err := SharedChecked(); err == nil {
		t.Fatal("a file that doesn't parse read as known")
	}
	if _, err := LoadChecked(); err == nil {
		t.Fatal("LoadChecked disagrees")
	}
}
