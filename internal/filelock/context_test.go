package filelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockContextCanceledBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created", "lock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unlock, held := LockContext(ctx, path, Wait)
	unlock()
	if held {
		t.Fatal("canceled context acquired lock")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled acquisition created folder: %v", err)
	}
}

func TestLockContextDeadlineAndReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, held := Lock(path, 0)
	if !held {
		t.Fatal("cannot hold real lock")
	}
	defer func() { release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	unlock, got := LockContext(ctx, path, Wait)
	unlock()
	if got || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("held=%v error=%v", got, ctx.Err())
	}
	if time.Since(start) > time.Second {
		t.Error("deadline did not interrupt lock wait")
	}
	release()
	release = func() {}
	unlock, got = LockContext(context.Background(), path, 0)
	if !got {
		t.Fatal("lock not reusable after canceled wait")
	}
	unlock()
}
