//go:build linux

package localdictionary

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/redact"
)

// Large cases require the fixed read-only job-container hard cap in this worker.
// GOMEMLIMIT is only a GC hint and is never accepted as containment evidence.
// OOM group 0 is not whole-tree termination: missing completion/OOM evidence
// fails the controller, and runner container removal is separately inspected.
func dictionaryValidationHardGuard(t *testing.T) string {
	t.Helper()
	if os.Getenv("DICTIONARY_RESOURCE_MODE") != "readonly-job-container" {
		t.Fatal("ENVIRONMENT NOT RUN: missing explicit container mode")
	}
	const group = "/sys/fs/cgroup"
	for name, want := range map[string]string{"memory.max": "2147483648", "memory.swap.max": "0"} {
		raw, err := os.ReadFile(filepath.Join(group, name))
		if err != nil || strings.TrimSpace(string(raw)) != want {
			t.Fatalf("ENVIRONMENT NOT RUN: missing hard control %s", name)
		}
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || strings.TrimSpace(string(raw)) != "0::/" {
		t.Fatal("ENVIRONMENT NOT RUN: worker is not in private container cgroup")
	}
	raw, err = os.ReadFile(filepath.Join(group, "memory.oom.group"))
	if err != nil || (strings.TrimSpace(string(raw)) != "0" && strings.TrimSpace(string(raw)) != "1") {
		t.Fatal("ENVIRONMENT NOT RUN: missing OOM group metadata")
	}
	raw, err = os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			status[key] = strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"Uid", "Gid"} {
		if strings.Join(strings.Fields(status[key]), " ") != "1001 1001 1001 1001" {
			t.Fatal("ENVIRONMENT NOT RUN: worker identity drift")
		}
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		n, err := strconv.ParseUint(status[key], 16, 64)
		if err != nil || n != 0 {
			t.Fatal("ENVIRONMENT NOT RUN: worker capability drift")
		}
	}
	if status["NoNewPrivs"] != "1" || status["Seccomp"] != "2" {
		t.Fatal("ENVIRONMENT NOT RUN: worker sandbox drift")
	}
	raw, err = os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	mounts := 0
	for _, line := range strings.Split(string(raw), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		fields, fs := strings.Fields(left), strings.Fields(right)
		if ok && len(fields) >= 6 && fields[4] == group {
			mounts++
			options := "," + fields[5] + ","
			if fields[3] != "/" || !strings.Contains(options, ",ro,") || strings.Contains(options, ",rw,") || len(fs) < 1 || fs[0] != "cgroup2" {
				t.Fatal("ENVIRONMENT NOT RUN: worker cgroup mount not read-only")
			}
		}
	}
	if mounts != 1 {
		t.Fatal("ENVIRONMENT NOT RUN: ambiguous worker cgroup mount")
	}
	raw, err = os.ReadFile(filepath.Join(group, "memory.events"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || seen[fields[0]] {
			t.Fatal("ENVIRONMENT NOT RUN: invalid memory events")
		}
		seen[fields[0]] = true
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value != 0 {
			t.Fatal("ENVIRONMENT NOT RUN: prior memory event; no pressure retry")
		}
	}
	if !seen["oom"] || !seen["oom_kill"] || !seen["oom_group_kill"] {
		t.Fatal("ENVIRONMENT NOT RUN: incomplete OOM evidence")
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
		m = nil
		words = nil
		runtime.GC()
		dictionaryValidationHardGuard(t)
		// Independently stress exact-count sorting with long shared suffixes.
		// Same full decoded-byte/count target; original process timeout still applies.
		shared := make([]string, 100000)
		for i := range shared {
			length := redact.MaxDictionaryBytes / len(shared)
			if i < redact.MaxDictionaryBytes%len(shared) {
				length++
			}
			prefix := fmt.Sprintf("synthetic-%06d-", i)
			shared[i] = prefix + strings.Repeat("x", length-len(prefix))
		}
		started := time.Now()
		compact, err := redact.CompileDictionary(shared)
		compileElapsed := time.Since(started)
		if err != nil {
			t.Fatal("shared-suffix compile failed", err)
		}
		for _, i := range []int{0, len(shared) / 2, len(shared) - 1} {
			if len(compact.Spans(shared[i])) != 1 {
				t.Fatal("shared-suffix match failed")
			}
		}
		t.Logf("resource shared_suffix_entries=%d decoded_bytes=%d compile_elapsed_ns=%d", len(shared), redact.MaxDictionaryBytes, compileElapsed.Nanoseconds())
		dictionaryValidationHeap(t, "shared-suffix")
		runtime.KeepAlive(compact)
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
