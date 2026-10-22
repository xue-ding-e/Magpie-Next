package sessions

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// This injects a WalkDir callback error, not an OS permission failure. All
// rollouts and persisted state are synthetic, and no permissions are changed.
func TestPortCodexRolloutsIncompleteScan(t *testing.T) {
	for _, failure := range []string{"readdir", "info", "walk"} {
		t.Run(failure, func(t *testing.T) { testIncompletePortScan(t, failure) })
	}
}

func testIncompletePortScan(t *testing.T, failure string) {
	home, hidden := portHome(t, portRolloutLines("scan retry summary"), false)
	data, err := os.ReadFile(hidden)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(hidden)
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	for _, dir := range []string{"a", "b"} {
		p := filepath.Join(home, "sessions", dir, filepath.Base(hidden))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		visible = append(visible, p)
	}
	// An unavailable old Pending entry must survive an incomplete scan too.
	oldPending := filepath.Join(home, "sessions", "unavailable", filepath.Base(hidden))
	oldSince := info.ModTime().Add(-time.Hour)
	st := codexPortState{Homes: map[string]codexPortHome{filepath.Clean(home): {Since: oldSince, Pending: []string{oldPending}}}}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(codexPortPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPortPath(), b, 0600); err != nil {
		t.Fatal(err)
	}
	injected := 0
	walker := func(root string, fn fs.WalkDirFunc) error {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if p == filepath.Dir(hidden) && err == nil && failure != "info" {
				injected++
				if failure == "readdir" {
					if e := fn(p, d, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrPermission}); e != nil {
						return e
					}
				}
				return fs.SkipDir
			}
			if p == hidden && err == nil && failure == "info" {
				injected++
				return fn(p, portScanBadInfo{d}, nil)
			}
			return fn(p, d, err)
		})
		if err == nil && failure == "walk" && root == filepath.Join(home, "sessions") {
			return &fs.PathError{Op: "walk", Path: root, Err: fs.ErrPermission}
		}
		return err
	}
	first, err := portCodexRollouts(context.Background(), home, walker)
	if err != nil {
		t.Fatal(err)
	}
	if injected != 1 {
		t.Fatalf("injected errors = %d, want 1", injected)
	}
	if first.Done {
		t.Error("incomplete scan reported Done")
	}
	if !reflect.DeepEqual(first.Rewritten, visible) {
		t.Errorf("discovered rollouts = %v, want %v", first.Rewritten, visible)
	}
	b, err = os.ReadFile(codexPortPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	was := st.Homes[filepath.Clean(home)]
	if !was.Since.Equal(oldSince) {
		t.Error("incomplete scan advanced watermark")
	}
	if !reflect.DeepEqual(was.Pending, []string{oldPending}) {
		t.Errorf("old pending lost: %v", was.Pending)
	}
	got, err := os.ReadFile(hidden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatal("hidden rollout changed")
	}
	second, err := PortCodexRollouts(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Done || !reflect.DeepEqual(second.Rewritten, []string{hidden}) {
		t.Errorf("previously hidden old rollout skipped: %+v", second)
	}
	third, err := PortCodexRollouts(context.Background(), home)
	if err != nil || !third.Done || len(third.Rewritten) != 0 {
		t.Errorf("repeat pass: %+v, %v", third, err)
	}
}

func TestPortCodexRolloutsMissingArchiveCompletes(t *testing.T) {
	home, path := portHome(t, portRolloutLines("missing archive is normal"), false)
	res, err := PortCodexRollouts(context.Background(), home)
	if err != nil || !res.Done || !reflect.DeepEqual(res.Rewritten, []string{path}) {
		t.Fatalf("missing archive: %+v, %v", res, err)
	}
}

// portScanBadInfo models metadata becoming unreadable after enumeration.
type portScanBadInfo struct{ fs.DirEntry }

func (d portScanBadInfo) Info() (fs.FileInfo, error) { return nil, fs.ErrPermission }
