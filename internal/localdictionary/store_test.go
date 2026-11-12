package localdictionary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/redact"
	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestBothLocalListsKeepOneRevision(t *testing.T) {
	fresh(t)
	masked, err := Update("new", "replace", "text", "synthetic-private")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := UpdateList("allow", masked.Revision, "replace", "text", "synthetic-public")
	if err != nil || allowed.Count != 1 || len(allowed.Words) != 0 {
		t.Fatal("allow-list metadata or write failed", err)
	}
	keep, err := Read(0, 100, true)
	if err != nil || len(keep.Words) != 1 || keep.Words[0] != "synthetic-private" || keep.Revision != allowed.Revision {
		t.Fatal("editing exceptions overwrote the mask list", err)
	}
	keep, err = ReadList("allow", 0, 100, true)
	if err != nil || len(keep.Words) != 1 || keep.Words[0] != "synthetic-public" {
		t.Fatal("exception readback failed", err)
	}
	if _, err = Update(masked.Revision, "replace", "text", "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal("a stale editor replaced the other list", err)
	}
	if _, err = UpdateList("unknown", allowed.Revision, "replace", "text", "wrong-list"); err == nil {
		t.Fatal("unknown list was accepted")
	}
}

func TestPageEditKeepsOtherPagesAndList(t *testing.T) {
	for _, target := range []string{"mask", "allow"} {
		t.Run(target, func(t *testing.T) {
			fresh(t)
			other := "allow"
			if target == "allow" {
				other = "mask"
			}
			kept, err := UpdateList(other, "new", "replace", "text", "other-list-kept")
			if err != nil {
				t.Fatal(err)
			}
			var original []string
			for i := range 205 {
				original = append(original, fmt.Sprintf("page-word-%03d", i))
			}
			before, err := UpdateList(target, kept.Revision, "replace", "text", strings.Join(original, "\n"))
			if err != nil {
				t.Fatal(err)
			}
			var edited = []string{" exact,\n新词 ", "", " exact,\n新词 ", "second-edited"}
			after, err := UpdatePage(target, before.Revision, 100, 100, edited)
			if err != nil || after.Count != 107 || len(after.Words) != 0 {
				t.Fatal("page edit failed or revealed entries in metadata", err)
			}
			first, err := ReadList(target, 0, 100, true)
			if err != nil || !slices.Equal(first.Words, original[:100]) {
				t.Fatal("editing a later page changed the first page", err)
			}
			last, err := ReadList(target, 100, 100, true)
			want := append([]string{edited[0], edited[3]}, original[200:]...)
			if err != nil || !slices.Equal(last.Words, want) {
				t.Fatal("page edit lost exact literals or the untouched tail", err)
			}
			otherList, err := ReadList(other, 0, 100, true)
			if err != nil || !slices.Equal(otherList.Words, []string{"other-list-kept"}) {
				t.Fatal("page edit changed the other list", err)
			}
			if _, err = UpdatePage(target, before.Revision, 100, 100, []string{"stale"}); !errors.Is(err, ErrConflict) {
				t.Fatal("a stale page edit overwrote a newer generation", err)
			}
			if _, err = UpdatePage(target, after.Revision, 100, 7, nil); err != nil {
				t.Fatal("could not delete the selected page", err)
			}
			first, err = ReadList(target, 0, 100, true)
			if err != nil || first.Count != 100 || !slices.Equal(first.Words, original[:100]) {
				t.Fatal("deleting one page deleted unrelated entries", err)
			}
		})
	}
}

func TestPageEditRejectsInvalidPageWithoutWriting(t *testing.T) {
	fresh(t)
	saved, err := Update("new", "replace", "text", "first\nsecond")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range [][2]int{{-1, 1}, {0, 0}, {0, 101}, {1, 2}, {3, 1}} {
		if _, err = UpdatePage("mask", saved.Revision, page[0], page[1], []string{"changed"}); err == nil {
			t.Fatalf("invalid page %v was accepted", page)
		}
	}
	after, err := os.ReadFile(Path())
	if err != nil || string(after) != string(before) {
		t.Fatal("an invalid page changed the saved generation", err)
	}
	var page PageWords
	if err = json.Unmarshal([]byte(`[" exact,\nword ","引号\"和反斜杠\\"]`), &page); err != nil || len(page) != 2 || page[0] != " exact,\nword " {
		t.Fatal("page JSON lost literal characters", err)
	}
	tooMany := "[" + strings.Repeat(`"",`, 100) + `""]`
	if err = json.Unmarshal([]byte(tooMany), &page); err == nil {
		t.Fatal("page decoder accepted more than 100 entries")
	}
}

func TestLegacyDictionaryLoadsAndKeepsExceptionsOnMaskUpdate(t *testing.T) {
	fresh(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(`{"revision":"legacy","words":["original"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ReadList("allow", 0, 100, true)
	if err != nil || s.Count != 0 || s.Revision != "legacy" {
		t.Fatal("legacy dictionaries must open with an empty exception list", err)
	}
	s, err = UpdateList("allow", s.Revision, "append", "json", `["保留🔑"," spaced "]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Update(s.Revision, "replace", "text", "updated"); err != nil {
		t.Fatal(err)
	}
	cache.Lock()
	cache.snapshot = nil
	cache.Unlock()
	s, err = ReadList("allow", 0, 100, true)
	if err != nil || strings.Join(s.Words, "|") != "保留🔑| spaced " {
		t.Fatal("mask-list save or restart lost the exceptions", err)
	}
}

func TestInvalidExceptionListIsNotSilentlyIgnored(t *testing.T) {
	for _, invalid := range []string{`null`, `{}`, `[1]`, `"not-an-array"`} {
		t.Run(invalid, func(t *testing.T) {
			fresh(t)
			if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			raw := `{"revision":"invalid-exception","words":["synthetic-private"],"allow":` + invalid + `}`
			if err := os.WriteFile(Path(), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Matchers(); err == nil {
				t.Fatal("invalid exceptions silently fell back to an empty list")
			}
			got, err := os.ReadFile(Path())
			if err != nil || string(got) != raw {
				t.Fatal("invalid dictionary file was modified")
			}
		})
	}
}
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
