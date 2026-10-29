package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filelock"
)

// This is a cancellation regression, not native encrypted-content compatibility.
// The lock is the real OS lock; stack observation only establishes that the
// worker reached the contended acquisition, without replacing production code.
func TestPortRolloutCanceledWhileCoordinationHeld(t *testing.T) {
	for _, upper := range []bool{false, true} {
		for _, releaseAtCancel := range []bool{false, true} {
			name := "rollout"
			if upper {
				name = "pass"
			}
			if releaseAtCancel {
				name += "/release-at-cancel"
			} else {
				name += "/held-after-cancel"
			}
			t.Run(name, func(t *testing.T) { testPortCanceled(t, upper, releaseAtCancel) })
		}
	}
}
func testPortCanceled(t *testing.T, upper, releaseAtCancel bool) {
	home, path := portHome(t, portRolloutLines("synthetic cancellation checkpoint")[:3], false)
	coordination := filepath.Join(home, "thread-writer-locks", ".coordination.lock")
	// Windows byte-range locks forbid reading the locked file. Snapshot before
	// acquiring the real lock, and verify it only after releasing that lock.
	if err := os.WriteFile(coordination, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state_5.sqlite", "session_index.jsonl", "rollout.before-magpie"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("synthetic unchanged sentinel\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	type saved struct {
		data  []byte
		mode  os.FileMode
		mtime time.Time
	}
	before := map[string]saved{}
	if err := filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		before[p] = saved{b, info.Mode(), info.ModTime()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unlock, held := filelock.Lock(coordination, 0)
	if !held {
		t.Fatal("cannot hold real coordination lock")
	}
	released := false
	release := func() {
		if !released {
			unlock()
			released = true
		}
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		changed bool
		err     error
		pass    CodexPort
	}
	done := make(chan result, 1)
	go func() {
		if upper {
			pass, err := PortCodexRollouts(ctx, home)
			done <- result{changed: len(pass.Rewritten) > 0, err: err, pass: pass}
		} else {
			changed, err := portRollout(ctx, home, path)
			done <- result{changed: changed, err: err}
		}
	}()
	// Do not leave a worker behind, including assertion-failure paths.
	defer func() {
		cancel()
		release()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		b := make([]byte, 1<<20)
		b = b[:runtime.Stack(b, true)]
		for _, stack := range bytes.Split(b, []byte("\n\n")) {
			if (bytes.Contains(stack, []byte("internal/filelock.Lock(")) || bytes.Contains(stack, []byte("internal/filelock.LockContext("))) && bytes.Contains(stack, []byte("internal/sessions.portRollout(")) {
				observed = true
				break
			}
		}
		if observed {
			break
		}
		runtime.Gosched()
	}
	if !observed {
		t.Fatal("worker never observed waiting in real coordination acquisition")
	}
	cancel()
	if releaseAtCancel {
		release()
	}
	var got result
	select {
	case got = <-done:
		done <- got
	case <-time.After(500 * time.Millisecond):
		t.Error("canceled worker did not return while coordination lock remained held")
		release()
		select {
		case got = <-done:
			done <- got
		case <-time.After(12 * time.Second):
			t.Fatal("worker did not return after release")
		}
	}
	if !upper && (got.changed || !errors.Is(got.err, context.Canceled)) {
		t.Errorf("canceled publication: changed=%v err=%v; want false/context.Canceled", got.changed, got.err)
	}
	if upper {
		if got.err != nil || got.changed || got.pass.Done || len(got.pass.Held) != 0 {
			t.Errorf("canceled pass: %+v err=%v", got.pass, got.err)
		}
		var st codexPortState
		b, err := os.ReadFile(codexPortPath())
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
		state := st.Homes[filepath.Clean(home)]
		if !state.Since.IsZero() || len(state.Pending) != 1 || state.Pending[0] != path {
			t.Errorf("canceled pending/watermark: %+v", state)
		}
	}
	release()
	for p, want := range before {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Error(err)
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Error(err)
			continue
		}
		if !bytes.Equal(b, want.data) || info.Mode() != want.mode || !info.ModTime().Equal(want.mtime) {
			t.Errorf("canceled operation modified %s", strings.TrimPrefix(p, home))
		}
	}
	if err := filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if _, ok := before[p]; !ok {
				t.Errorf("unexpected published/temp file %s", p)
			}
		}
		return nil
	}); err != nil {
		t.Error(err)
	}
}
