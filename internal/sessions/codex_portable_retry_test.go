package sessions

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/codexsummary"
)

// A failed read must remain eligible after the scan watermark advances. Restoring
// the same old mtime models recovery from a backup, not a newly appended rollout.
func TestPortCodexRolloutsRetriesFailedRead(t *testing.T) {
	home, path := portHome(t, portRolloutLines("Synthetic retry summary"), true)
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, original.Mode()); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	bad := []byte("not a zstd stream: synthetic fixture only")
	write(bad)
	first, err := PortCodexRollouts(context.Background(), home)
	if err != nil || len(first.Rewritten) != 0 {
		t.Fatalf("failed input should not be rewritten: %+v, %v", first, err)
	}
	stillBad, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stillBad, bad) {
		t.Fatal("failed input changed")
	}
	write(good)
	second, err := PortCodexRollouts(context.Background(), home)
	if err != nil || len(second.Rewritten) != 1 || second.Rewritten[0] != path {
		t.Fatalf("repaired old rollout was skipped after read failure: %+v, %v", second, err)
	}
	lines := readRollout(t, path)
	if strings.Contains(lines[2], codexsummary.Mark) || !strings.Contains(lines[2], "Synthetic retry summary") {
		t.Fatal("retry did not preserve and expand the synthetic summary")
	}
	third, err := PortCodexRollouts(context.Background(), home)
	if err != nil || len(third.Rewritten) != 0 {
		t.Fatalf("successful retry was not idempotent: %+v, %v", third, err)
	}
}
