// Package localdictionary keeps an opt-in literal dictionary on this machine.
// It is deliberately outside Settings and backup.Bundle: password dictionaries
// must not join setup synchronization or an ordinary settings export.
package localdictionary

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/redact"
	"github.com/yetone/magpie/internal/steady"
)

var ErrConflict = errors.New("dictionary changed; reload before saving")

// JSON escaping may use six bytes per input byte. The decoded library has a
// separate 16 MiB budget; this bound also applies before decoding disk data.
const MaxEncodedBytes = 6*(16<<20) + 100000*4 + 1024

type document struct {
	Revision string   `json:"revision"`
	Words    []string `json:"words"`
}
type State struct {
	Revision string   `json:"revision"`
	Count    int      `json:"count"`
	Bytes    int      `json:"bytes"`
	Words    []string `json:"words,omitempty"`
}
type snapshot struct {
	doc     document
	matcher *redact.Dictionary
	info    os.FileInfo
	bytes   int
	path    string
}

var cache struct {
	sync.Mutex
	snapshot *snapshot
}

func Path() string { return filepath.Join(appdir.Config(), "local-sensitive-dictionary.json") }

// load must hold cache. Invalid or unreadable files are errors, never an empty
// dictionary: the caller holds the request back rather than sending secrets.
func load() (*snapshot, error) {
	p := Path()
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		if old := cache.snapshot; old != nil && old.path == p && len(old.doc.Words) > 0 {
			return nil, errors.New("enabled local dictionary is missing; restore it before continuing")
		}
		return &snapshot{doc: document{Revision: "new"}, path: p}, nil
	}
	if err != nil || !fi.Mode().IsRegular() || (runtime.GOOS != "windows" && (fi.Mode().Perm()&0077 != 0 || fi.Mode().Perm()&0400 == 0)) {
		return nil, errors.New("cannot read local dictionary")
	}
	if old := cache.snapshot; old != nil && old.path == p && old.info != nil && os.SameFile(old.info, fi) && old.info.Mode() == fi.Mode() && old.info.Size() == fi.Size() && old.info.ModTime() == fi.ModTime() {
		return old, nil
	}
	if fi.Size() > MaxEncodedBytes {
		return nil, errors.New("local dictionary exceeds file size limit")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, errors.New("cannot read local dictionary")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxEncodedBytes+1))
	if err != nil || len(b) > MaxEncodedBytes {
		return nil, errors.New("cannot read local dictionary")
	}
	var raw struct {
		Revision string          `json:"revision"`
		Words    json.RawMessage `json:"words"`
	}
	if !utf8.Valid(b) || json.Unmarshal(b, &raw) != nil || raw.Revision == "" {
		return nil, errors.New("invalid local dictionary; repair or restore the file before continuing")
	}
	parsed, err := parseJSONWords(raw.Words)
	if err != nil {
		return nil, err
	}
	d := document{Revision: raw.Revision}
	words, size, err := normalize(parsed)
	if err != nil {
		return nil, err
	}
	d.Words = words
	m, err := redact.CompileDictionary(words)
	if err != nil {
		return nil, err
	}
	s := &snapshot{doc: d, matcher: m, info: fi, bytes: size, path: p}
	cache.snapshot = s
	return s, nil
}
func normalize(words []string) ([]string, int, error) {
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, len(words))
	size := 0
	for _, w := range words {
		if w == "" {
			continue
		}
		if !utf8.ValidString(w) {
			return nil, 0, errors.New("dictionary must be valid UTF-8")
		}
		if !seen[w] {
			size += len(w)
			if size > redact.MaxDictionaryBytes {
				return nil, 0, errors.New("dictionary exceeds 16 MiB")
			}
			if len(out) >= redact.MaxDictionaryWords {
				return nil, 0, errors.New("dictionary exceeds 100000 entries")
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	return out, size, nil
}
func state(s *snapshot, offset, limit int, reveal bool) State {
	out := State{Revision: s.doc.Revision, Count: len(s.doc.Words), Bytes: s.bytes}
	if reveal {
		offset = min(max(offset, 0), len(s.doc.Words))
		limit = min(max(limit, 1), 100)
		out.Words = append([]string{}, s.doc.Words[offset:min(offset+limit, len(s.doc.Words))]...)
	}
	return out
}
func Read(offset, limit int, reveal bool) (State, error) {
	cache.Lock()
	defer cache.Unlock()
	s, e := load()
	if e != nil {
		return State{}, e
	}
	return state(s, offset, limit, reveal), nil
}
func Matcher() (*redact.Dictionary, error) {
	cache.Lock()
	defer cache.Unlock()
	s, e := load()
	if e != nil {
		return nil, e
	}
	return s.matcher, nil
}
func Wanted() bool { s, e := Read(0, 0, false); return e != nil || s.Count > 0 }

// Update is a compare-and-swap across app and CLI processes. Failure leaves the
// last generation intact. The lock must actually be held (never best effort).
func Update(revision, mode, format, content string) (State, error) {
	if len(content) > redact.MaxDictionaryBytes || !utf8.ValidString(content) {
		return State{}, errors.New("import must be UTF-8 and at most 16 MiB")
	}
	content = strings.TrimPrefix(content, "\ufeff")
	var incoming []string
	switch format {
	case "json":
		var err error
		incoming, err = parseJSONWords([]byte(content))
		if err != nil {
			return State{}, errors.New("import must be a JSON string array")
		}
	case "text":
		if strings.Count(content, "\n") > redact.MaxDictionaryWords {
			return State{}, errors.New("import exceeds 100000 lines")
		}
		incoming = strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	default:
		return State{}, errors.New("unknown import format")
	}
	if mode != "append" && mode != "replace" {
		return State{}, errors.New("unknown import mode")
	}
	cache.Lock()
	defer cache.Unlock()
	unlock, held := filelock.Lock(Path()+".lock", 0)
	defer unlock()
	if !held {
		return State{}, errors.New("dictionary is busy or cannot be locked; retry after checking storage")
	}
	s, e := load()
	if e != nil {
		return State{}, e
	}
	if revision != s.doc.Revision {
		return State{}, ErrConflict
	}
	if mode == "append" {
		incoming = append(append([]string{}, s.doc.Words...), incoming...)
	}
	words, size, e := normalize(incoming)
	if e != nil {
		return State{}, e
	}
	m, e := redact.CompileDictionary(words)
	if e != nil {
		return State{}, e
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return State{}, errors.New("cannot create dictionary revision")
	}
	d := document{Revision: hex.EncodeToString(nonce[:]), Words: words}
	b, e := json.Marshal(d)
	if e != nil {
		return State{}, errors.New("cannot encode dictionary")
	}
	if e = writePrivate(Path(), b); e != nil {
		return State{}, errors.New("cannot save dictionary; previous generation retained")
	}
	fi, e := os.Stat(Path())
	if e != nil {
		return State{}, errors.New("dictionary saved but cannot be verified; reload")
	}
	cache.snapshot = &snapshot{doc: d, matcher: m, info: fi, path: Path(), bytes: size}
	return state(cache.snapshot, 0, 0, false), nil
}

// Never follow links or update hard-linked files in place. A new private
// generation is complete and synced before its name replaces the old one.
var replaceFile = steady.Rename

func writePrivate(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".local-dictionary-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = steady.Sync(f); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return replaceFile(name, path)
}

// Bound decoded entries before allocating a giant slice for an otherwise small
// JSON array of empty strings. Diagnostic errors never echo literal contents.
func parseJSONWords(b []byte) ([]string, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('[') {
		return nil, errors.New("dictionary must be a JSON string array")
	}
	out := make([]string, 0)
	for d.More() {
		if len(out) >= redact.MaxDictionaryWords {
			return nil, errors.New("dictionary exceeds 100000 entries")
		}
		value, err := d.Token()
		w, isString := value.(string)
		if err != nil || !isString {
			return nil, errors.New("dictionary must be a JSON string array")
		}
		out = append(out, w)
	}
	if _, e = d.Token(); e != nil {
		return nil, errors.New("invalid dictionary array")
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("invalid dictionary array")
	}
	return out, nil
}
