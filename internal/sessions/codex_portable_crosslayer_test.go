package sessions

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yetone/magpie/internal/legacycodex"
)

// This composes real in-memory modules, not the production portable writer.
// The preview's 5425d3e and reference planner's 970111f contracts overlap for
// this non-attached paginated subset: immutable rollout ID, exclusive ordinal,
// decoded byte cutoff, and metadata at K. It is not a full native schema claim.
// Opaque reasoning below is an artificial preservation sentinel, NOT proof of
// valid native encryption or provider resume. No history or credentials are read.
func TestSyntheticCompactionPreviewReferenceComposition(t *testing.T) {
	for _, tc := range []struct {
		name, summary string
		grow          bool
	}{
		{"shrink", "  合成摘要\n", false},
		{"grow", strings.Repeat("<", 1200) + "合成", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the documented synthetic producer format; do not use conversion
			// output as the expected bytes, which would make the assertion circular.
			plain := []byte(`{"summary":"` + strings.ReplaceAll(tc.summary, "\n", `\n`) + `","model":"","created_at":1}`)
			if !json.Valid(plain) {
				t.Fatal("invalid fixture JSON")
			}
			key := sha256.Sum256([]byte("ai-toolbox-remote-compaction"))
			block, err := aes.NewCipher(key[:])
			if err != nil {
				t.Fatal(err)
			}
			gcm, err := cipher.NewGCM(block)
			if err != nil {
				t.Fatal(err)
			}
			nonce := []byte("synthetic123")
			capsule := "ai-toolbox-compact-v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, plain, nil)...))
			originalItem := `{"type":"compaction","id":"cmp_synthetic","encrypted_content":"` + capsule + `"}`
			text, err := json.Marshal("Context summary from previous turns:\n" + tc.summary)
			if err != nil {
				t.Fatal(err)
			}
			expectedItem := `{"content":[{"text":` + string(text) + `,"type":"output_text"}],"role":"assistant","type":"message"}`
			header := func(thread, parent string, ordinal, offset int) string {
				base := ""
				if parent != "" {
					base = fmt.Sprintf(`,"history_base":{"thread_id":%q,"end_ordinal_exclusive":%d,"end_byte_offset":%d}`, parent, ordinal, offset)
				}
				return fmt.Sprintf("{\"ordinal\":%d,\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"session_id\":%q,\"history_mode\":\"paginated\"%s}}\n", ordinal, thread, stagedSession, base)
			}
			meta := header(stagedThread, "", 0, 0)
			wrap := func(item string) string { return `{"ordinal":1,"type":"response_item","payload":` + item + "}\r\n" }
			// These bytes must survive verbatim, including summary, call linkage,
			// whitespace, UTF-8 and CRLF. The child inherits the tool pair as a prefix.
			conserved := `{"ordinal":2,"type":"response_item","payload":{"type":"reasoning","id":"rs_synthetic","encrypted_content":"OPAQUE_SYNTHETIC_NOT_REAL","summary":[{"type":"summary_text","text":"保留"}]}}` + "\r\n" +
				`{"ordinal":3,"type":"response_item","payload":{"type":"function_call","name":"synthetic","arguments":"{}","call_id":"call_synthetic"}}` + "\n" +
				`{"ordinal":4,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_synthetic","output":"synthetic"}}` + "\n"
			tail := `{"ordinal":5,"type":"response_item","payload":{"type":"message","role":"user","content":[]}}` + "\n"
			oldPrefix, newPrefix := meta+wrap(originalItem)+conserved, meta+wrap(expectedItem)+conserved
			before, after := []byte(oldPrefix+tail), []byte(newPrefix+tail)
			if (len(after) > len(before)) != tc.grow {
				t.Fatal("fixture lacks intended byte length direction")
			}
			preview, err := legacycodex.PreviewAIToolboxRollout(before)
			if err != nil {
				t.Fatal(err)
			}
			if preview.SourceSHA256 != sha256.Sum256(before) || preview.ProposedSHA256 != sha256.Sum256(after) || preview.ConvertedItems != 1 {
				t.Fatal("real preview disagrees with independent exact-byte candidate")
			}
			mapped, ok := preview.ProposedCutoff(uint64(len(oldPrefix)), 5)
			if !ok || mapped != uint64(len(newPrefix)) {
				t.Fatal("prefix cutoff disagrees")
			}
			childOld := []byte(header(stagedChild, stagedRootB, 5, len(oldPrefix)) + `{"ordinal":6,"type":"response_item","payload":{"type":"message","role":"user","content":[]}}` + "\n")
			childNew := []byte(header(stagedChild, stagedRootB, 5, len(newPrefix)) + `{"ordinal":6,"type":"response_item","payload":{"type":"message","role":"user","content":[]}}` + "\n")
			single, err := legacycodex.PreviewAIToolboxHistoryBase(stagedRootB, sha256.Sum256(before), before, bytes.SplitAfter(childOld, []byte("\n"))[0])
			if err != nil {
				t.Fatal(err)
			}
			if single.ProposedEnd != mapped || single.EndOrdinalExclusive != 5 {
				t.Fatal("single-reference preview disagrees with root preview")
			}
			graph := stagedFixture(t)
			for i := range graph.rollouts {
				v := &graph.rollouts[i]
				switch v.claimedRolloutID {
				case stagedRootA:
					v.before = bytes.Clone(before)
					v.after = bytes.Clone(before)
				case stagedRootB:
					v.before = before
					v.after = after
				case stagedChild:
					v.before = childOld
					v.after = childNew
				case stagedGrandchild:
					v.before = []byte(header(stagedGrandchild, stagedChild, 7, len(childOld)))
					v.after = []byte(header(stagedGrandchild, stagedChild, 7, len(childNew)))
				}
			}
			source := fstest.MapFS{"sessions": {Mode: fs.ModeDir | 0700}, "archived_sessions": {Mode: fs.ModeDir | 0700}}
			request := syntheticInventoryRequest{schemaPin: referenceSchemaPin, activeRoot: "sessions", archivedRoot: "archived_sessions"}
			for _, v := range graph.rollouts {
				root := "sessions"
				if v.claimedRolloutID == stagedChild || v.claimedRolloutID == stagedGrandchild {
					root = "archived_sessions"
				}
				name := path.Join(root, v.plainBasename)
				source[name] = &fstest.MapFile{Data: bytes.Clone(v.before), Mode: 0600}
				request.candidates = append(request.candidates, syntheticRolloutCandidate{name, sha256.Sum256(v.before), bytes.Clone(v.after)})
			}
			result, err := validateSyntheticSnapshotPlan(source, request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.references.changedRootPayloads, []string{stagedRootB}) || len(result.references.entries) != 4 {
				t.Fatal("composed plan lost root semantic review or revisions")
			}
			expectedByID := make(map[string]stagedRolloutBytes, len(graph.rollouts))
			for _, expected := range graph.rollouts {
				expectedByID[expected.claimedRolloutID] = expected
			}
			for _, entry := range result.references.entries {
				expected, exists := expectedByID[entry.rolloutID]
				if !exists {
					t.Fatal("composed plan returned unknown or duplicate revision")
				}
				delete(expectedByID, entry.rolloutID)
				if entry.beforeHash != sha256.Sum256(expected.before) || entry.afterHash != sha256.Sum256(expected.after) || entry.beforeBytes != uint64(len(expected.before)) || entry.afterBytes != uint64(len(expected.after)) {
					t.Fatal("composed plan lost exact revision bytes")
				}
			}
			if len(expectedByID) != 0 {
				t.Fatal("composed plan omitted expected revision")
			}
			// Wrong-revision old byte mapping must not pass merely because that revision
			// shares the stable thread ID. Keep the source fixture otherwise identical.
			stale := request
			stale.candidates = append([]syntheticRolloutCandidate(nil), request.candidates...)
			for i := range stale.candidates {
				if strings.HasSuffix(stale.candidates[i].relativePath, stagedChild+".jsonl") {
					stale.candidates[i].after = bytes.Clone(childOld)
				} else if strings.HasSuffix(stale.candidates[i].relativePath, stagedGrandchild+".jsonl") {
					// Keep descendants internally consistent with the stale child;
					// rejection must come from its wrong parent revision mapping.
					stale.candidates[i].after = bytes.Clone(source[stale.candidates[i].relativePath].Data)
				}
			}
			if _, err := validateSyntheticSnapshotPlan(source, stale); err == nil {
				t.Fatal("accepted old parent-revision cutoff after real summary expansion")
			}
			if !bytes.Equal(before, []byte(oldPrefix+tail)) {
				t.Fatal("real modules mutated immutable input")
			}
		})
	}
}
