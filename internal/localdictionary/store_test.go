package localdictionary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/redact"
	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }
func fresh(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cache.Lock()
	cache.snapshot = nil
	cache.Unlock()
}
func TestImportCASAndLiteralRoundTrip(t *testing.T) {
	fresh(t)
	s, e := Read(0, 100, false)
	if e != nil || s.Revision != "new" {
		t.Fatal(s, e)
	}
	s, e = Update(s.Revision, "replace", "text", "\ufeffa,b\r\n padded \r\n秘密\n\n")
	if e != nil || s.Count != 3 {
		t.Fatal(s, e)
	}
	if len(s.Words) != 0 {
		t.Fatal("metadata exposed words")
	}
	p, e := Read(0, 100, true)
	if e != nil || strings.Join(p.Words, "|") != "a,b| padded |秘密" {
		t.Fatal(p, e)
	}
	_, e = Update("new", "replace", "text", "different")
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	p, e = Read(1, 1, true)
	if e != nil || len(p.Words) != 1 || p.Words[0] != " padded " {
		t.Fatal(p, e)
	}
	before := p.Revision
	_, e = Update(before, "replace", "json", "not-json")
	if e == nil {
		t.Fatal("accepted malformed import")
	}
	p, _ = Read(0, 0, false)
	if p.Revision != before {
		t.Fatal("failed import changed generation")
	}
	fi, e := os.Stat(Path())
	if e != nil || (runtime.GOOS != "windows" && fi.Mode().Perm()&0077 != 0) {
		t.Fatal(fi, e)
	}
}
func TestImportConcurrentCAS(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "json", `["initial"]`)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, v := range []string{"first", "second"} {
		wg.Add(1)
		go func(v string) { defer wg.Done(); _, e := Update(s.Revision, "append", "text", v); results <- e }(v)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
func TestCorruptDictionaryFailsClosed(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "text", "synthetic")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(Path(), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Matcher(); e == nil {
		t.Fatal("corruption became an empty dictionary")
	}
	if !Wanted() {
		t.Fatal("disabled protection on corruption")
	}
	if _, e = Update(s.Revision, "replace", "text", "overwrite"); e == nil {
		t.Fatal("overwrote corrupt file")
	}
}
func TestImportLimits(t *testing.T) {
	fresh(t)
	for _, v := range []string{string([]byte{255}), strings.Repeat("x", (16<<20)+1)} {
		if _, e := Update("new", "replace", "text", v); e == nil {
			t.Fatal("accepted invalid import")
		}
	}
	if _, e := os.Stat(Path()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed import created dictionary", e)
	}
}
func TestImportEmptyAndDuplicate(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "json", `["one","one"]`)
	if e != nil || s.Count != 1 || s.Bytes != 3 {
		t.Fatal(s, e)
	}
	s, e = Update(s.Revision, "replace", "text", "")
	if e != nil || s.Count != 0 {
		t.Fatal(s, e)
	}
	cache.Lock()
	cache.snapshot = nil
	cache.Unlock()
	s, e = Read(0, 100, true)
	if e != nil || s.Count != 0 {
		t.Fatal(s, e)
	}
}
func TestMissingEnabledDictionaryFailsClosed(t *testing.T) {
	fresh(t)
	_, e := Update("new", "replace", "text", "synthetic")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(Path()); e != nil {
		t.Fatal(e)
	}
	if _, e = Matcher(); e == nil {
		t.Fatal("lost library became disabled")
	}
}
func TestImportTooManyEmptyLines(t *testing.T) {
	fresh(t)
	if _, e := Update("new", "replace", "text", strings.Repeat("\n", 100002)); e == nil {
		t.Fatal("unbounded line count")
	}
}
func TestHardlinkUpdateDoesNotModifyOtherName(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "text", "original")
	if e != nil {
		t.Fatal(e)
	}
	other := Path() + ".other"
	if e = os.Link(Path(), other); e != nil {
		t.Skip("hardlinks unavailable")
	}
	before, e := os.ReadFile(other)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Update(s.Revision, "replace", "text", "replacement"); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(other)
	if e != nil || string(before) != string(after) {
		t.Fatal("updated another hardlink", e)
	}
}
func TestFailedFirstWriteCanRetry(t *testing.T) {
	fresh(t)
	original := replaceFile
	replaceFile = func(string, string) error { return errors.New("synthetic rename failure") }
	t.Cleanup(func() { replaceFile = original })
	if _, e := Update("new", "replace", "text", "private"); e == nil {
		t.Fatal("expected failed save")
	}
	if _, e := os.Stat(Path()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed first write left a broken target", e)
	}
	replaceFile = original
	if _, e := Update("new", "replace", "text", "private"); e != nil {
		t.Fatal(e)
	}
}
func TestFailedReplaceKeepsOldGeneration(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "text", "old synthetic")
	if e != nil {
		t.Fatal(e)
	}
	original := replaceFile
	replaceFile = func(string, string) error { return errors.New("synthetic rename failure") }
	t.Cleanup(func() { replaceFile = original })
	if _, e := Update(s.Revision, "replace", "text", "new synthetic"); e == nil {
		t.Fatal("expected failed save")
	}
	got, e := Read(0, 100, true)
	if e != nil || got.Revision != s.Revision || got.Words[0] != "old synthetic" {
		t.Fatal(got, e)
	}
}
func TestImportNullEntriesRejected(t *testing.T) {
	fresh(t)
	s, e := Update("new", "replace", "text", "keep synthetic")
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`[null]`, `[1]`, `[{}]`, `[[]]`} {
		if _, e = Update(s.Revision, "replace", "json", bad); e == nil {
			t.Fatal("accepted non-string JSON element")
		}
	}
	got, e := Read(0, 100, true)
	if e != nil || got.Revision != s.Revision {
		t.Fatal("invalid import changed generation", e)
	}
	if e = os.WriteFile(Path(), []byte(`{"revision":"corrupt","words":[null]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Matcher(); e == nil {
		t.Fatal("corrupt schema silently disabled dictionary")
	}
}

func TestImport100000AppendPagingAndGeneration(t *testing.T) {
	fresh(t)
	words := make([]string, redact.MaxDictionaryWords)
	for i := range words {
		words[i] = fmt.Sprintf("synthetic-store-%06d", i)
	}
	raw, err := json.Marshal(words)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Update("new", "replace", "json", string(raw))
	if err != nil || s.Count != len(words) || len(s.Words) != 0 {
		t.Fatal("100k import failed", s.Count, err)
	}
	old, err := Matcher()
	if err != nil {
		t.Fatal(err)
	}
	page, err := Read(99900, 1000, true)
	if err != nil || len(page.Words) != 100 || page.Words[99] != words[99999] {
		t.Fatal("reveal did not stay on a bounded page", err)
	}
	s, err = Update(s.Revision, "append", "text", words[0])
	if err != nil || s.Count != len(words) {
		t.Fatal("append counted a duplicate as a new entry", err)
	}
	if _, err = Update(s.Revision, "append", "text", "one-too-many"); err == nil {
		t.Fatal("append exceeded the entry limit")
	}
	after, err := Read(0, 0, false)
	if err != nil || after.Revision != s.Revision {
		t.Fatal("failed append changed the generation", err)
	}
	if _, err = Update(s.Revision, "replace", "text", "new-generation"); err != nil {
		t.Fatal(err)
	}
	current, err := Matcher()
	if err != nil || len(current.Spans(words[0])) != 0 || len(old.Spans(words[0])) != 1 {
		t.Fatal("replacement changed an in-flight generation", err)
	}
}

func TestImportRequiresWriterLock(t *testing.T) {
	fresh(t)
	s, err := Update("new", "replace", "text", "synthetic-initial")
	if err != nil {
		t.Fatal(err)
	}
	unlock, held := filelock.Lock(Path()+".lock", 0)
	defer unlock()
	if !held {
		t.Fatal("could not hold the synthetic writer lock")
	}
	if _, err = Update(s.Revision, "append", "text", "synthetic-next"); err == nil {
		t.Fatal("save bypassed the cross-process writer lock")
	}
	after, err := Read(0, 100, true)
	if err != nil || after.Revision != s.Revision || after.Count != 1 {
		t.Fatal("busy save changed the generation", err)
	}
}
