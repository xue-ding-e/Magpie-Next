package filelock

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LockContext is Lock with a cancellable wait. A cancelled operation never
// acquires or publishes through a lock after its caller has gone away.
func LockContext(ctx context.Context, path string, wait time.Duration) (func(), bool) {
	if ctx.Err() != nil {
		return func() {}, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		}
	}
	if err != nil {
		return func() {}, false
	}
	start := time.Now()
	for {
		if ctx.Err() != nil {
			f.Close()
			return func() {}, false
		}
		got, err := TryLock(f)
		if err != nil {
			f.Close()
			return func() {}, false
		}
		if got {
			if ctx.Err() != nil {
				Unlock(f)
				f.Close()
				return func() {}, false
			}
			return func() { Unlock(f); f.Close() }, true
		}
		if time.Since(start) >= wait {
			f.Close()
			return func() {}, false
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return func() {}, false
		case <-timer.C:
		}
	}
}
