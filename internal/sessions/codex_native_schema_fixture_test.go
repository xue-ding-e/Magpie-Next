//go:build magpie_native_schema_validation

package sessions

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yetone/magpie/internal/legacycodex"
)

// matchNativeFixturePreview is shared by the positive contract and its two
// preservation mutants. A passing graph/schema alone cannot waive exact bytes.
func matchNativeFixturePreview(before, after []byte) (legacycodex.RolloutPreview, error) {
	preview, err := legacycodex.PreviewAIToolboxRollout(before)
	if err != nil {
		return preview, err
	}
	if preview.ConvertedItems != 1 || preview.SourceSHA256 != sha256.Sum256(before) || preview.ProposedSHA256 != sha256.Sum256(after) {
		return preview, fmt.Errorf("independent typed output disagrees with real preview")
	}
	return preview, nil
}

// requireNativeFixtureRevisions is the independent four-revision fixture
// completeness contract. Neither the official scanner nor an arbitrary fs.FS
// can infer a terminal descendant absent from its supplied snapshot.
func requireNativeFixtureRevisions(plan readOnlyReferencePlan) error {
	var ids []string
	for _, entry := range plan.entries {
		ids = append(ids, entry.rolloutID)
	}
	if !reflect.DeepEqual(ids, []string{stagedRootA, stagedRootB, stagedChild, stagedGrandchild}) {
		return fmt.Errorf("synthetic four-revision inventory is incomplete")
	}
	return nil
}

// TestNativeSchemaSharedFixture consumes bytes emitted and admitted by the
// independently pinned official Codex parser. It never invokes a real client,
// opens a user session, or connects preview helpers to the production writer.
// The control must supply this fixture; missing preparation is NOT a skipped pass.
func TestNativeSchemaSharedFixture(t *testing.T) {
	const root = "testdata/native-schema-generated"
	manifest, err := os.ReadFile(filepath.Join(root, "sha256.json"))
	if err != nil {
		t.Fatal("SETUP: official-parser fixture is required: ", err)
	}
	var hashes map[string]string
	if err := json.Unmarshal(manifest, &hashes); err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 16 {
		t.Fatal("expected two variants, four revisions and two phases")
	}
	for _, variant := range []string{"shrink", "grow"} {
		t.Run(variant, func(t *testing.T) {
			var source = fstest.MapFS{"sessions": {Mode: fs.ModeDir | 0700}, "archived_sessions": {Mode: fs.ModeDir | 0700}}
			var request = syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: "sessions", archivedRoot: "archived_sessions"}
			var beforeByID = make(map[string][]byte)
			var afterByID = make(map[string][]byte)
			var pathByID = make(map[string]string)
			for _, revision := range []string{stagedRootA, stagedRootB, stagedChild, stagedGrandchild} {
				thread, directory := stagedThread, "sessions"
				if revision == stagedChild || revision == stagedGrandchild {
					thread, directory = revision, "archived_sessions"
				}
				suffix := thread
				if thread != revision {
					suffix += "_" + revision
				}
				name := path.Join(directory, "rollout-2026-10-31T00-00-00-"+suffix+".jsonl")
				pathByID[revision] = name
				for _, phase := range []string{"before", "after"} {
					relative := path.Join(variant, phase, name)
					data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
					if err != nil {
						t.Fatal(err)
					}
					hash := sha256.Sum256(data)
					if hashes[relative] != hex.EncodeToString(hash[:]) {
						t.Fatal("official parser byte identity mismatch: ", relative)
					}
					if phase == "before" {
						beforeByID[revision] = data
						source[name] = &fstest.MapFile{Data: data, Mode: 0600}
					} else {
						afterByID[revision] = data
					}
				}
				request.candidates = append(request.candidates, syntheticRolloutCandidate{name, sha256.Sum256(beforeByID[revision]), afterByID[revision]})
			}
			before, after := beforeByID[stagedRootB], afterByID[stagedRootB]
			preview, err := matchNativeFixturePreview(before, after)
			if err != nil {
				t.Fatal(err)
			}
			for _, sentinel := range []string{"OPAQUE_SYNTHETIC_NOT_REAL", "call_synthetic"} {
				mutant := bytes.Replace(after, []byte(sentinel), []byte("ALTERED_SYNTHETIC"), 1)
				if bytes.Equal(mutant, after) {
					t.Fatal("preservation negative control did not mutate its target")
				}
				if _, err := matchNativeFixturePreview(before, mutant); err == nil {
					t.Fatal("accepted altered reasoning/tool linkage: ", sentinel)
				}
			}
			mapped, ok := preview.ProposedCutoff(uint64(len(before)), 5)
			if !ok || mapped != uint64(len(after)) {
				t.Fatal("decoded prefix cutoff mismatch")
			}
			oldLines, newLines := bytes.SplitAfter(before, []byte("\n")), bytes.SplitAfter(after, []byte("\n"))
			if len(oldLines) != 6 || !bytes.Equal(oldLines[0], newLines[0]) || !bytes.Equal(bytes.Join(oldLines[2:], nil), bytes.Join(newLines[2:], nil)) {
				t.Fatal("metadata, reasoning or tool bytes changed")
			}
			// Independent semantic sentinels ensure conservation is not vacuously empty.
			for _, sentinel := range []string{"OPAQUE_SYNTHETIC_NOT_REAL", "保留", "call_synthetic", "synthetic result"} {
				if !bytes.Contains(before, []byte(sentinel)) {
					t.Fatal("missing preservation sentinel")
				}
			}
			if strings.Count(string(before), `"call_id":"call_synthetic"`) != 2 {
				t.Fatal("tool linkage not paired")
			}
			result, err := validateSyntheticSnapshotPlan(source, request)
			if err != nil {
				t.Fatal(err)
			}
			if requireNativeFixtureRevisions(result.references) != nil || !reflect.DeepEqual(result.references.changedRootPayloads, []string{stagedRootB}) {
				t.Fatal("four-revision plan lost identity")
			}
			for _, entry := range result.references.entries {
				if entry.beforeHash != sha256.Sum256(beforeByID[entry.rolloutID]) || entry.afterHash != sha256.Sum256(afterByID[entry.rolloutID]) {
					t.Fatal("revision hash mismatch")
				}
			}
			// Duplicate revision: native index keeps one; Magpie admission rejects it.
			var duplicateSource = make(fstest.MapFS, len(source)+1)
			for name, file := range source {
				duplicateSource[name] = file
			}
			duplicatePath := path.Join("archived_sessions", path.Base(pathByID[stagedRootB]))
			duplicateSource[duplicatePath] = source[pathByID[stagedRootB]]
			duplicate := request
			duplicate.candidates = append([]syntheticRolloutCandidate(nil), request.candidates...)
			duplicate.candidates = append(duplicate.candidates, syntheticRolloutCandidate{duplicatePath, sha256.Sum256(beforeByID[stagedRootB]), afterByID[stagedRootB]})
			if _, err := validateSyntheticSnapshotPlan(duplicateSource, duplicate); err == nil {
				t.Fatal("admission accepted duplicate immutable revision")
			}
			for _, omitted := range []string{stagedChild, stagedGrandchild} {
				var missingSource = make(fstest.MapFS, len(source)-1)
				for name, file := range source {
					if name != pathByID[omitted] {
						missingSource[name] = file
					}
				}
				missing := request
				missing.candidates = nil
				for _, candidate := range request.candidates {
					if candidate.relativePath != pathByID[omitted] {
						missing.candidates = append(missing.candidates, candidate)
					}
				}
				partial, err := validateSyntheticSnapshotPlan(missingSource, missing)
				if omitted == stagedChild {
					if err == nil {
						t.Fatal("admission accepted dangling grandchild")
					}
				} else {
					// Missing terminal leaf is NOT claimed detectable by native scan
					// or planner alone: our explicit expected-four contract rejects it.
					if err != nil {
						t.Fatal("unexpected orphan-free partial snapshot refusal: ", err)
					}
					if requireNativeFixtureRevisions(partial.references) == nil {
						t.Fatal("fixture completeness gate missed omitted terminal descendant")
					}
				}
			}
			// Keep child/grandchild mutually consistent but tied to the OLD B cutoff.
			// This must fail even though unchanged A shares its old bytes/thread ID.
			stale := request
			stale.candidates = append([]syntheticRolloutCandidate(nil), request.candidates...)
			for i := range stale.candidates {
				if stale.candidates[i].relativePath == pathByID[stagedChild] || stale.candidates[i].relativePath == pathByID[stagedGrandchild] {
					stale.candidates[i].after = source[stale.candidates[i].relativePath].Data
				}
			}
			if _, err := validateSyntheticSnapshotPlan(source, stale); err == nil {
				t.Fatal("accepted wrong immutable parent revision cutoff")
			}
			// Native parser admission does not grant permission to migrate attached forks.
			attached := request
			attached.candidates = append([]syntheticRolloutCandidate(nil), request.candidates...)
			for i := range attached.candidates {
				if attached.candidates[i].relativePath == pathByID[stagedChild] {
					attached.candidates[i].after = bytes.Replace(attached.candidates[i].after, []byte(`"history_mode":`), []byte(`"attached_thread":true,"history_mode":`), 1)
				}
			}
			if _, err := validateSyntheticSnapshotPlan(source, attached); err == nil {
				t.Fatal("accepted unsupported attached relation")
			}
		})
	}
}
