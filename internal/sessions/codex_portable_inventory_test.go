package sessions

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// All fixtures are synthetic MapFS values, immutable during validation.
// They establish no live filesystem or native Codex safety guarantee.

type inventoryFixtureCandidate = syntheticRolloutCandidate

type inventoryFixture struct {
	source                   fstest.MapFS
	activeRoot, archivedRoot string
	candidates               []inventoryFixtureCandidate
}

type inventoryFixtureCase struct {
	name    string
	fixture inventoryFixture
	// reader may add deterministic failures to fixture.source without disk I/O.
	reader     fs.FS
	wantReject bool
	reason     string
}

func newSyntheticInventoryFixture(t *testing.T) inventoryFixture {
	t.Helper()
	// Reuse the existing correct two-root-revision/child/grandchild graph and
	// exact reference-byte candidates, rather than inventing a second format.
	staged := stagedFixture(t)
	var fixture = inventoryFixture{
		source:     fstest.MapFS{"sessions": {Mode: fs.ModeDir | 0700}, "archived_sessions": {Mode: fs.ModeDir | 0700}},
		activeRoot: "sessions", archivedRoot: "archived_sessions",
	}
	for _, item := range staged.rollouts {
		directory := "sessions/2026/10/26"
		if item.claimedRolloutID == stagedChild || item.claimedRolloutID == stagedGrandchild {
			directory = "archived_sessions/2026/10/26"
		}
		relative := path.Join(directory, item.plainBasename)
		fixture.source[relative] = &fstest.MapFile{Data: bytes.Clone(item.before), Mode: 0600}
		fixture.candidates = append(fixture.candidates, inventoryFixtureCandidate{
			relativePath: relative, expectedBeforeHash: sha256.Sum256(item.before), after: bytes.Clone(item.after),
		})
	}
	return fixture
}

// syntheticInventoryCases declares the supported snapshot boundary. The test
// below runs each case through the scanner and composed reference validator;
// coverage remains limited to these artificial immutable inputs.
func syntheticInventoryCases(t *testing.T) []inventoryFixtureCase {
	t.Helper()
	var cases []inventoryFixtureCase
	add := func(name, reason string, change func(*inventoryFixture)) {
		fixture := newSyntheticInventoryFixture(t)
		if change != nil {
			change(&fixture)
		}
		cases = append(cases, inventoryFixtureCase{name: name, fixture: fixture, reader: fixture.source, wantReject: change != nil, reason: reason})
	}
	add("complete-active-and-archive", "accept configured synthetic roots only; result must still list changed root payload for semantic review", nil)
	add("missing-archive", "an absent expected root must not mean empty history", func(f *inventoryFixture) {
		for name := range f.source {
			if name == f.archivedRoot || strings.HasPrefix(name, f.archivedRoot+"/") {
				delete(f.source, name)
			}
		}
	})
	add("omitted-candidate", "every discovered rollout requires an explicit staged or unchanged candidate", func(f *inventoryFixture) { f.candidates = f.candidates[1:] })
	add("extra-candidate", "candidate absent from discovery must not be accepted", func(f *inventoryFixture) {
		extra := f.candidates[0]
		extra.relativePath = "sessions/not-discovered.jsonl"
		f.candidates = append(f.candidates, extra)
	})
	add("duplicate-candidate", "do not collapse duplicate candidate paths into a map", func(f *inventoryFixture) { f.candidates = append(f.candidates, f.candidates[0]) })
	add("source-hash-mismatch", "candidate must refer to the exact observed original bytes", func(f *inventoryFixture) { f.candidates[0].expectedBeforeHash[0] ^= 1 })
	add("duplicate-immutable-id", "two physical paths for the same revision are ambiguous even if byte-identical", func(f *inventoryFixture) {
		old := f.candidates[2]
		duplicate := old
		duplicate.relativePath = path.Join("archived_sessions/duplicate", path.Base(old.relativePath))
		file := *f.source[old.relativePath]
		file.Data = bytes.Clone(file.Data)
		f.source[duplicate.relativePath] = &file
		f.candidates = append(f.candidates, duplicate)
	})
	add("plain-and-zstd", "this subset rejects compressed siblings rather than copying native skip precedence", func(f *inventoryFixture) {
		name := f.candidates[0].relativePath + ".zst"
		f.source[name] = &fstest.MapFile{Data: []byte("synthetic unsupported compressed bytes"), Mode: 0600}
	})
	add("symlink-entry", "never follow or silently skip a link", func(f *inventoryFixture) {
		f.source["sessions/link"] = &fstest.MapFile{Mode: fs.ModeSymlink | 0600, Data: []byte("outside")}
	})
	add("symlink-root", "root entry mode must be checked, not just a following Stat", func(f *inventoryFixture) {
		f.source[f.activeRoot] = &fstest.MapFile{Mode: fs.ModeSymlink | 0700, Data: []byte("elsewhere")}
	})
	add("overlapping-roots", "ancestor/descendant roots are not independent coverage", func(f *inventoryFixture) {
		f.archivedRoot = "sessions/archive"
		f.source[f.archivedRoot] = &fstest.MapFile{Mode: fs.ModeDir | 0700}
	})
	add("identical-roots", "one root cannot stand for active and archive coverage", func(f *inventoryFixture) { f.archivedRoot = f.activeRoot })
	add("unknown-file", "unknown names are unsupported, not invisible", func(f *inventoryFixture) {
		f.source["sessions/unknown.data"] = &fstest.MapFile{Data: []byte("synthetic"), Mode: 0600}
	})
	add("malformed-rollout", "successful traversal does not make unreadable metadata acceptable", func(f *inventoryFixture) {
		name := f.candidates[0].relativePath
		f.source[name].Data = []byte("{\n")
		f.candidates[0].expectedBeforeHash = sha256.Sum256(f.source[name].Data)
	})
	for _, kind := range []string{"read-dir", "open", "read", "close", "close-dir", "info"} {
		fixture := newSyntheticInventoryFixture(t)
		file := fixture.candidates[0].relativePath
		var reader = inventoryFaultFS{FS: fixture.source}
		switch kind {
		case "read-dir":
			reader.readDirAt = "archived_sessions/2026/10/26"
		case "open":
			reader.openAt = file
		case "read":
			reader.readAt = file
		case "close":
			reader.closeAt = file
		case "close-dir":
			reader.closeAt = fixture.archivedRoot
		case "info":
			reader.infoAt = file
		}
		cases = append(cases, inventoryFixtureCase{name: "error-" + kind, fixture: fixture, reader: reader, wantReject: true, reason: "injected I/O error must abort without a partial complete plan"})
	}
	return cases
}

var errInventoryFixtureIO = errors.New("synthetic inventory I/O failure")

type inventoryFaultFS struct {
	fs.FS
	readDirAt, openAt, readAt, closeAt, infoAt string
}

func (f inventoryFaultFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.FS, name)
	if name == f.readDirAt {
		return entries, errInventoryFixtureIO
	}
	return entries, err
}

func (f inventoryFaultFS) Open(name string) (fs.File, error) {
	if name == f.openAt {
		return nil, errInventoryFixtureIO
	}
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &inventoryFaultFile{File: file, failRead: name == f.readAt, failClose: name == f.closeAt, failReadDir: name == f.readDirAt, directoryName: name, failInfoAt: f.infoAt}, nil
}

type inventoryFaultFile struct {
	fs.File
	failRead, failClose, failReadDir bool
	directoryName, failInfoAt        string
}

func (f *inventoryFaultFile) Read(buffer []byte) (int, error) {
	n, err := f.File.Read(buffer)
	if f.failRead {
		return n, errInventoryFixtureIO
	}
	return n, err
}

func (f *inventoryFaultFile) Close() error {
	err := f.File.Close()
	if f.failClose {
		return errInventoryFixtureIO
	}
	return err
}

func (f *inventoryFaultFile) ReadDir(n int) ([]fs.DirEntry, error) {
	reader, ok := f.File.(fs.ReadDirFile)
	if !ok {
		return nil, errInventoryFixtureIO
	}
	entries, err := reader.ReadDir(n)
	for i, entry := range entries {
		if path.Join(f.directoryName, entry.Name()) == f.failInfoAt {
			entries[i] = inventoryBadInfoEntry{entry}
		}
	}
	if f.failReadDir {
		return entries, errInventoryFixtureIO
	}
	return entries, err
}

func TestSyntheticSnapshotInventory(t *testing.T) {
	for _, test := range syntheticInventoryCases(t) {
		t.Run(test.name, func(t *testing.T) {
			request := syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: test.fixture.activeRoot, archivedRoot: test.fixture.archivedRoot, candidates: test.fixture.candidates}
			result, err := validateSyntheticSnapshotPlan(test.reader, request)
			if (err != nil) != test.wantReject {
				t.Fatalf("%s: err=%v", test.reason, err)
			}
			if test.wantReject {
				if len(result.paths) != 0 || len(result.references.entries) != 0 {
					t.Fatal("partial plan escaped failure")
				}
				return
			}
			if len(result.paths) != 4 || len(result.references.entries) != 4 || len(result.references.changedRootPayloads) != 1 {
				t.Fatal("incomplete composition or lost root semantic-review flag")
			}
		})
	}
}

func TestSyntheticSnapshotEmptyRoots(t *testing.T) {
	source := fstest.MapFS{"sessions": {Mode: fs.ModeDir | 0700}, "archived_sessions": {Mode: fs.ModeDir | 0700}, "outside.txt": {Data: []byte("outside configured roots")}}
	result, err := validateSyntheticSnapshotPlan(source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: "sessions", archivedRoot: "archived_sessions"})
	if err != nil || len(result.paths) != 0 {
		t.Fatalf("empty configured roots: %v", err)
	}
}

func TestSyntheticSnapshotReadBounds(t *testing.T) {
	file := fstest.MapFS{"synthetic": {Data: []byte("1234")}}
	if _, err := readSyntheticRollout(file, "synthetic", 3); err == nil {
		t.Fatal("read exceeded bounded budget")
	}
	if got, err := readSyntheticRollout(file, "synthetic", 4); err != nil || string(got) != "1234" {
		t.Fatal("exact bounded read rejected")
	}
	directory := fstest.MapFS{"a": {Data: []byte{}}, "b": {Data: []byte{}}}
	if _, err := readSyntheticDirectory(directory, ".", 1); err == nil {
		t.Fatal("entry budget exceeded silently")
	}
	if got, err := readSyntheticDirectory(directory, ".", 2); err != nil || len(got) != 2 {
		t.Fatal("exact entry budget rejected")
	}
}

type inventoryBadInfoEntry struct{ fs.DirEntry }

func (entry inventoryBadInfoEntry) Info() (fs.FileInfo, error) { return nil, errInventoryFixtureIO }

// A broken filesystem adapter must not turn a dot entry into repeated walks.
type inventoryDotEntry struct{ fs.DirEntry }

func (entry inventoryDotEntry) Name() string { return "." }

type inventoryDotFS struct {
	fs.FS
	opens *int
}

func (source inventoryDotFS) Open(name string) (fs.File, error) {
	if name == "sessions" {
		*source.opens++
	}
	file, err := source.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &inventoryDotFile{File: file, inject: name == "sessions"}, nil
}

type inventoryDotFile struct {
	fs.File
	inject bool
}

func (file *inventoryDotFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := file.File.(fs.ReadDirFile).ReadDir(n)
	if file.inject && len(entries) > 0 {
		entries[0] = inventoryDotEntry{entries[0]}
	}
	return entries, err
}
func TestSyntheticSnapshotRejectsDotEntry(t *testing.T) {
	fixture := newSyntheticInventoryFixture(t)
	opens := 0
	source := inventoryDotFS{FS: fixture.source, opens: &opens}
	_, err := validateSyntheticSnapshotPlan(source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: fixture.activeRoot, archivedRoot: fixture.archivedRoot, candidates: fixture.candidates})
	if err == nil || opens != 1 {
		t.Fatalf("dot entry was not rejected on first visit: opens=%d err=%v", opens, err)
	}
}

func TestSyntheticSnapshotRejectsStaleChildMetadataOrdinal(t *testing.T) {
	fixture := newSyntheticInventoryFixture(t)
	// The leaf grandchild's history_base cutoff stays exclusive ordinal 4,
	// but both old/new metadata records falsely claim ordinal 5. No descendant
	// could incidentally detect this mismatch through another boundary lookup.
	candidate := &fixture.candidates[0]
	fixture.source[candidate.relativePath].Data = bytes.Replace(fixture.source[candidate.relativePath].Data, []byte(`"ordinal":4`), []byte(`"ordinal":5`), 1)
	candidate.after = bytes.Replace(candidate.after, []byte(`"ordinal":4`), []byte(`"ordinal":5`), 1)
	candidate.expectedBeforeHash = sha256.Sum256(fixture.source[candidate.relativePath].Data)
	if result, err := validateSyntheticSnapshotPlan(fixture.source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: fixture.activeRoot, archivedRoot: fixture.archivedRoot, candidates: fixture.candidates}); err == nil || len(result.references.entries) != 0 {
		t.Fatal("accepted child metadata ordinal inconsistent with history_base")
	}
}

func TestSyntheticSnapshotRejectsConservedInvalidUTF8(t *testing.T) {
	fixture := newSyntheticInventoryFixture(t)
	candidate := &fixture.candidates[0]
	inject := func(data []byte) []byte {
		return bytes.Replace(data, []byte(`"history_mode":`), []byte("\"unknown\":\"\xff\",\"history_mode\":"), 1)
	}
	fixture.source[candidate.relativePath].Data = inject(fixture.source[candidate.relativePath].Data)
	candidate.after = inject(candidate.after)
	candidate.expectedBeforeHash = sha256.Sum256(fixture.source[candidate.relativePath].Data)
	if _, err := validateSyntheticSnapshotPlan(fixture.source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: fixture.activeRoot, archivedRoot: fixture.archivedRoot, candidates: fixture.candidates}); err == nil {
		t.Fatal("accepted invalid UTF-8 through inventory admission")
	}
}

func TestSyntheticSnapshotResultDoesNotRetainInputs(t *testing.T) {
	fixture := newSyntheticInventoryFixture(t)
	result, err := validateSyntheticSnapshotPlan(fixture.source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: fixture.activeRoot, archivedRoot: fixture.archivedRoot, candidates: fixture.candidates})
	if err != nil {
		t.Fatal(err)
	}
	saved := result
	saved.paths = append([]string(nil), result.paths...)
	saved.references.entries = append([]readOnlyRolloutSummary(nil), result.references.entries...)
	saved.references.changedRootPayloads = append([]string(nil), result.references.changedRootPayloads...)
	for _, file := range fixture.source {
		for i := range file.Data {
			file.Data[i] = 'x'
		}
	}
	for _, candidate := range fixture.candidates {
		for i := range candidate.after {
			candidate.after[i] = 'y'
		}
	}
	if !reflect.DeepEqual(result, saved) {
		t.Fatal("snapshot report retained mutable input")
	}
}

func TestSyntheticSnapshotInitialOrdinalBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		index  int
		value  string
		reject bool
	}{
		{"root-zero", 3, "0", false}, {"root-nonzero", 3, "1", true},
		{"child-cutoff", 0, "4", false}, {"child-cutoff-plus-one", 0, "5", true},
		{"child-missing", 0, "", true}, {"child-null", 0, "null", true},
		{"child-max-exclusive-overflow", 0, "18446744073709551615", true},
		{"child-out-of-range", 0, "18446744073709551616", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSyntheticInventoryFixture(t)
			candidate := &fixture.candidates[test.index]
			mutate := func(data []byte) []byte {
				if test.index == 3 {
					if test.value == "1" {
						data = bytes.Replace(data, []byte(`"ordinal":1`), []byte(`"ordinal":2`), 1)
					}
					return bytes.Replace(data, []byte(`"ordinal":0`), []byte(`"ordinal":`+test.value), 1)
				}
				if test.value == "" {
					return bytes.Replace(data, []byte(`"ordinal":4,`), nil, 1)
				}
				return bytes.Replace(data, []byte(`"ordinal":4`), []byte(`"ordinal":`+test.value), 1)
			}
			fixture.source[candidate.relativePath].Data = mutate(fixture.source[candidate.relativePath].Data)
			candidate.after = mutate(candidate.after)
			candidate.expectedBeforeHash = sha256.Sum256(fixture.source[candidate.relativePath].Data)
			result, err := validateSyntheticSnapshotPlan(fixture.source, syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: fixture.activeRoot, archivedRoot: fixture.archivedRoot, candidates: fixture.candidates})
			if (err != nil) != test.reject {
				t.Fatalf("initial ordinal admission: %v", err)
			}
			if test.reject && len(result.references.entries) != 0 {
				t.Fatal("partial result on ordinal rejection")
			}
		})
	}
}
