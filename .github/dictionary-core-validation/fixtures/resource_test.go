//go:build linux

package localdictionary

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"

	"github.com/yetone/magpie/internal/redact"
)

// Large cases require proof that this process has entered an owned cgroup.
// GOMEMLIMIT is only a GC hint and is never accepted as containment evidence.
func dictionaryValidationHardGuard(t *testing.T) string {
	t.Helper()
	group := os.Getenv("DICTIONARY_RESOURCE_CGROUP")
	if group == "" || !strings.HasPrefix(filepath.Base(group), "magpie-dictionary-") {
		t.Fatal("ENVIRONMENT NOT RUN: no owned cgroup hard limit")
	}
	for name, want := range map[string]string{"memory.max": "2147483648", "memory.swap.max": "0", "memory.oom.group": "1"} {
		raw, err := os.ReadFile(filepath.Join(group, name))
		if err != nil || strings.TrimSpace(string(raw)) != want {
			t.Fatalf("ENVIRONMENT NOT RUN: missing hard control %s", name)
		}
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(raw)), "/"+filepath.Base(group)) {
		t.Fatal("worker is not in guarded cgroup")
	}
	return os.Getenv("DICTIONARY_RESOURCE_STAGE")
}

func dictionaryValidationWords(n, total, generation int) []string {
	words := make([]string, n)
	for i := range words {
		length := total / n
		if i < total%n {
			length++
		}
		// Different pseudorandom suffixes prevent an unrealistically compact
		// all-shared trie. Fixed seeds and IDs are synthetic, not credentials.
		prefix := fmt.Sprintf("synthetic-%d-%06d-", generation, i)
		b := make([]byte, length)
		copy(b, prefix)
		seed := uint64(i+1)*6364136223846793005 + uint64(generation+1)
		for j := len(prefix); j < len(b); j++ {
			seed = seed*2862933555777941757 + 3037000493
			b[j] = 'a' + byte((seed>>32)%26)
		}
		words[i] = string(b)
	}
	return words
}
func dictionaryValidationHeap(t *testing.T, stage string) uint64 {
	t.Helper()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	t.Logf("resource max_rss_bytes=%d", usage.Maxrss*1024)
	t.Logf("resource stage=%s live_heap_bytes=%d heap_sys_bytes=%d total_alloc_bytes=%d", stage, m.HeapAlloc, m.HeapSys, m.TotalAlloc)
	return m.HeapAlloc
}

func TestDictionaryValidationResource(t *testing.T) {
	stage := dictionaryValidationHardGuard(t)
	fresh(t)
	switch stage {
	case "100k":
		words := dictionaryValidationWords(100000, 2400000, 0)
		s, err := Update("new", "replace", "text", strings.Join(words, "\n"))
		if err != nil || s.Count != 100000 {
			t.Fatal("100k normal save failed", err)
		}
		m, err := Matcher()
		if err != nil || len(m.Spans(words[99999])) != 1 {
			t.Fatal("100k saved matcher failed")
		}
		dictionaryValidationHeap(t, stage)
		runtime.KeepAlive(m)
	case "16mib":
		words := dictionaryValidationWords(100000, redact.MaxDictionaryBytes, 0)
		m, err := redact.CompileDictionary(words)
		if err != nil || len(m.Spans(words[99999])) != 1 {
			t.Fatal("exact 16 MiB decoded matcher failed", err)
		}
		dictionaryValidationHeap(t, stage)
		runtime.KeepAlive(m)
	case "held", "released":
		// Imports have a separate 16 MiB content limit, so count newlines too.
		// Log the distinction rather than claiming 16 MiB decoded via import.
		total := redact.MaxDictionaryBytes - 99999
		first := dictionaryValidationWords(100000, total, 1)
		s, err := Update("new", "replace", "text", strings.Join(first, "\n"))
		if err != nil {
			t.Fatal("old generation save failed", err)
		}
		old, err := Matcher()
		if err != nil {
			t.Fatal(err)
		}
		oldProbe := first[99999]
		first = nil
		second := dictionaryValidationWords(100000, total, 2)
		next, err := Update(s.Revision, "replace", "text", strings.Join(second, "\n"))
		if err != nil {
			t.Fatal("replacement save failed", err)
		}
		current, err := Matcher()
		if err != nil || next.Revision == s.Revision || old == current || len(old.Spans(oldProbe)) != 1 || len(current.Spans(second[99999])) != 1 || len(current.Spans(oldProbe)) != 0 {
			t.Fatal("held old and current generations are not independent")
		}
		second = nil
		runtime.GC()
		held := dictionaryValidationHeap(t, "held")
		t.Logf("resource import_bytes=%d decoded_bytes=%d entries=%d", redact.MaxDictionaryBytes, total, 100000)
		runtime.KeepAlive(old)
		if stage == "released" {
			old = nil
			runtime.GC()
			debug.FreeOSMemory()
			released := dictionaryValidationHeap(t, "released")
			if released+(1<<20) >= held {
				t.Fatal("released old generation was not reclaimed")
			}
		}
		runtime.KeepAlive(current)
	default:
		t.Fatal("unknown guarded resource stage")
	}
}
