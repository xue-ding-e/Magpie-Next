package legacycodex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const historyBaseSourceID = "00000000-0000-4000-8000-00000000000a"
const historyBaseParentThreadID = "00000000-0000-4000-8000-000000000001"
const historyBaseChildID = "00000000-0000-4000-8000-000000000002"

func historyBaseHeader(ordinal, offset uint64) string {
	meta := strings.ReplaceAll(previewMeta, historyBaseParentThreadID, historyBaseChildID)
	meta = strings.Replace(meta, `"ordinal":0`, fmt.Sprintf(`"ordinal":%d`, ordinal), 1)
	return strings.TrimSuffix(meta, "}}") + fmt.Sprintf(`,"history_base":{"thread_id":%q,"end_ordinal_exclusive":%d,"end_byte_offset":%d}}}`,
		historyBaseSourceID, ordinal, offset) + "\n"
}

func historyBaseForkFields(header string, ordinal uint64) string {
	return strings.TrimSuffix(header, "}}\n") + fmt.Sprintf(`,"forked_from_id":%q,"forked_from_ordinal_exclusive":%d}}`, historyBaseParentThreadID, ordinal) + "\n"
}

func assertHistoryBaseError(t *testing.T, sourceID, parent, child, code string) {
	t.Helper()
	parentBytes, childBytes := []byte(parent), []byte(child)
	got, err := PreviewAIToolboxHistoryBase(sourceID, sha256.Sum256(parentBytes), parentBytes, childBytes)
	var failure *HistoryBasePreviewError
	if !errors.As(err, &failure) || failure.Code != code || !reflect.DeepEqual(got, HistoryBasePreview{}) {
		t.Fatalf("expected zero report and %s, got report=%+v error=%v", code, got, err)
	}
	if string(parentBytes) != parent || string(childBytes) != child {
		t.Fatal("failure mutated input")
	}
	if strings.Contains(fmt.Sprintf("%#v %v", err, err), "PRIVATE_MARKER") {
		t.Fatal("error contains input")
	}
}

func TestHistoryBasePreviewMapsDecodedPrefixes(t *testing.T) {
	growingText, shrinkingText := strings.Repeat("<>&\u2028", 30), strings.Repeat("synthetic summary ", 100)
	growing, shrinking := previewRawItem(t, growingText), previewKnownItem(t, shrinkingText)
	for name, items := range map[string][]string{
		"no_conversion": {`{"type":"reasoning","id":"rs_synthetic","encrypted_content":"OPAQUE_SYNTHETIC","summary":[]}`},
		"growing":       {growing},
		"shrinking":     {shrinking},
		"mixed": {growing, `{"type":"function_call","call_id":"call_synthetic","name":"synthetic","arguments":"{}"}`,
			shrinking, `{"type":"function_call_output","call_id":"call_synthetic","output":"合成 😀"}`},
	} {
		t.Run(name, func(t *testing.T) {
			parent, expected := previewMeta+"\n", previewMeta+"\n"
			for i, item := range items {
				ending := "\n"
				if i%2 == 0 {
					ending = "\r\n"
				}
				line := fmt.Sprintf(`{"ordinal":%d,"type":"response_item","payload":%s}`, i+1, item) + ending
				parent += line
				line = strings.Replace(line, growing, previewExpectedItem(t, growingText), 1)
				line = strings.Replace(line, shrinking, previewExpectedItem(t, shrinkingText), 1)
				expected += line
			}
			oldLines, newLines := strings.SplitAfter(parent, "\n"), strings.SplitAfter(expected, "\n")
			var oldEnd, newEnd uint64
			for i := 0; i < len(oldLines)-1; i++ {
				oldEnd += uint64(len(oldLines[i]))
				newEnd += uint64(len(newLines[i]))
				child := historyBaseHeader(uint64(i+1), oldEnd)
				if i%2 != 0 {
					child = historyBaseForkFields(child, uint64(i+1))
				} else {
					child = strings.TrimSuffix(child, "\n") + "\r\n"
				}
				parentBytes, childBytes := []byte(parent), []byte(child)
				got, err := PreviewAIToolboxHistoryBase(strings.ToUpper(historyBaseSourceID), sha256.Sum256(parentBytes), parentBytes, childBytes)
				if err != nil {
					t.Fatal(err)
				}
				if got.Version != 1 || got.SourceSHA256 != sha256.Sum256([]byte(parent)) || got.ProposedSHA256 != sha256.Sum256([]byte(expected)) ||
					got.ChildHeaderSHA256 != sha256.Sum256([]byte(child)) || got.SourceBytes != uint64(len(parent)) || got.ProposedBytes != uint64(len(expected)) ||
					got.EndOrdinalExclusive != uint64(i+1) || got.SourceEnd != oldEnd || got.ProposedEnd != newEnd {
					t.Fatal("native-shaped reference did not match independent decoded-byte boundaries")
				}
				wantConversions := uint64(1)
				if name == "no_conversion" {
					wantConversions = 0
				} else if name == "mixed" {
					wantConversions = 2
				}
				if got.ConvertedItems != wantConversions || string(parentBytes) != parent || string(childBytes) != child {
					t.Fatal("conversion count or original input changed")
				}
				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				for _, marker := range []string{historyBaseSourceID, historyBaseParentThreadID, historyBaseChildID, growingText, shrinkingText, "OPAQUE_SYNTHETIC", "call_synthetic"} {
					if strings.Contains(string(encoded)+fmt.Sprintf("%+v %#v", got, got), marker) {
						t.Fatal("report exposes identity or history content")
					}
				}
			}
		})
	}
}

func TestHistoryBasePreviewRejectsStaleSourceBinding(t *testing.T) {
	parent := []byte(previewMeta + "\n")
	child := []byte(historyBaseHeader(1, uint64(len(parent))))
	expected := sha256.Sum256(parent)
	changed := bytes.Replace(parent, []byte("/synthetic"), []byte("/synthEtic"), 1)
	for name, input := range map[string][]byte{"same_size_rewrite": changed, "append": append(bytes.Clone(parent), '\n')} {
		t.Run(name, func(t *testing.T) {
			before, headerBefore := bytes.Clone(input), bytes.Clone(child)
			got, err := PreviewAIToolboxHistoryBase(historyBaseSourceID, expected, input, child)
			var failure *HistoryBasePreviewError
			if !errors.As(err, &failure) || failure.Code != "source_hash_mismatch" || !reflect.DeepEqual(got, HistoryBasePreview{}) {
				t.Fatal("stale source hash was accepted")
			}
			if !bytes.Equal(before, input) || !bytes.Equal(headerBefore, child) {
				t.Fatal("source binding failure mutated input")
			}
		})
	}
	assertHistoryBaseError(t, historyBaseParentThreadID, string(parent), string(child), "source_id_mismatch")
	assertHistoryBaseError(t, "PRIVATE_MARKER", string(parent), string(child), "unsupported_source_id")
}

func TestHistoryBasePreviewRejectsCutoffMismatch(t *testing.T) {
	parent := strings.Replace(previewMeta, "/synthetic", "/synthetic/合成 😀", 1) + "\n" + `{"ordinal":1,"type":"response_item","payload":` + previewKnownItem(t, "PRIVATE_MARKER 合成 😀") + "}\r\n"
	for name, coordinates := range map[string][2]uint64{
		"zero":              {0, 0},
		"zero_byte":         {2, 0},
		"zero_ordinal":      {0, uint64(len(parent))},
		"past_ordinal":      {3, uint64(len(parent))},
		"max_ordinal":       {^uint64(0), uint64(len(parent))},
		"past_eof":          {2, uint64(len(parent) + 1)},
		"max_byte":          {2, ^uint64(0)},
		"before_final_lf":   {2, uint64(len(parent) - 1)},
		"rune_not_byte":     {2, uint64(utf8.RuneCountInString(parent))},
		"wrong_record_end":  {2, uint64(strings.IndexByte(parent, '\n') + 1)},
		"wrong_ordinal_end": {1, uint64(len(parent))},
	} {
		t.Run(name, func(t *testing.T) {
			assertHistoryBaseError(t, historyBaseSourceID, parent, historyBaseHeader(coordinates[0], coordinates[1]), "cutoff_mismatch")
		})
	}
	child := historyBaseHeader(2, uint64(len(parent)))
	for _, ordinal := range []string{"1", "3", "null", "-1", "2.0"} {
		t.Run("child_ordinal_"+ordinal, func(t *testing.T) {
			assertHistoryBaseError(t, historyBaseSourceID, parent, strings.Replace(child, `"ordinal":2`, `"ordinal":`+ordinal, 1), "child_ordinal_mismatch")
		})
	}
	for _, key := range []string{"end_ordinal_exclusive", "end_byte_offset"} {
		for _, number := range []string{`"2"`, "null", "-0", "-1", "2.0", "2e0", "18446744073709551616"} {
			t.Run(key+number, func(t *testing.T) {
				old := fmt.Sprintf(`"%s":%d`, key, map[string]uint64{"end_ordinal_exclusive": 2, "end_byte_offset": uint64(len(parent))}[key])
				assertHistoryBaseError(t, historyBaseSourceID, parent, strings.Replace(child, old, `"`+key+`":`+number, 1), "unsupported_history_base")
			})
		}
	}
}

func TestHistoryBasePreviewRejectsAmbiguousHeader(t *testing.T) {
	parent := previewMeta + "\n" + `{"ordinal":1,"type":"response_item","payload":` + previewKnownItem(t, "PRIVATE_MARKER") + "}\n"
	child := historyBaseHeader(2, uint64(len(parent)))
	for name, tc := range map[string]struct{ header, code string }{
		"empty":             {"", "unsupported_child_header"},
		"incomplete_tail":   {strings.TrimSuffix(child, "\n"), "unsupported_child_header"},
		"two_headers":       {child + child, "unsupported_child_header"},
		"malformed":         {"{PRIVATE_MARKER\n", "child_invalid_json"},
		"duplicate":         {strings.Replace(child, `"ordinal":2`, `"ordinal":2,"ordinal":2`, 1), "child_duplicate_key"},
		"escaped_duplicate": {strings.Replace(child, `"ordinal":2`, `"ordinal":2,"\u006frdinal":2`, 1), "child_duplicate_key"},
		"unknown_duplicate": {strings.Replace(child, `"ordinal":2`, `"ordinal":2,"PRIVATE_MARKER":{"x":1,"x":2}`, 1), "child_duplicate_key"},
		"bad_utf8":          {strings.Replace(child, "synthetic", "\xff", 1), "child_invalid_json"},
		"bad_surrogate":     {strings.Replace(child, "synthetic", `\uD800`, 1), "child_invalid_json"},
		"wrong_type":        {strings.Replace(child, `"type":"session_meta"`, `"type":"response_item"`, 1), "unsupported_child_header"},
		"legacy":            {strings.Replace(child, "paginated", "legacy", 1), "unsupported_child_history"},
		"missing_mode":      {strings.Replace(child, `,"history_mode":"paginated"`, "", 1), "unsupported_child_history"},
		"unknown_envelope":  {strings.Replace(child, `"ordinal":2`, `"ordinal":2,"PRIVATE_MARKER":true`, 1), "unsupported_child_metadata"},
		"unknown_metadata":  {strings.Replace(child, `"history_mode":"paginated"`, `"history_mode":"paginated","PRIVATE_MARKER":{}`, 1), "unsupported_child_metadata"},
		"unknown_base":      {strings.Replace(child, `"end_byte_offset":`, `"PRIVATE_MARKER":0,"end_byte_offset":`, 1), "unsupported_history_base"},
		"missing_base":      {historyBaseHeaderWithoutBase(), "unsupported_history_base"},
		"null_base":         {strings.Replace(historyBaseHeaderWithoutBase(), `"history_mode":"paginated"`, `"history_mode":"paginated","history_base":null`, 1), "unsupported_history_base"},
		"array_base":        {strings.Replace(historyBaseHeaderWithoutBase(), `"history_mode":"paginated"`, `"history_mode":"paginated","history_base":[]`, 1), "unsupported_history_base"},
		"renamed_byte":      {strings.Replace(child, "end_byte_offset", "PRIVATE_MARKER", 1), "unsupported_history_base"},
		"wrong_base_id":     {strings.Replace(child, historyBaseSourceID, "PRIVATE_MARKER", 1), "unsupported_history_base"},
		"role_alias":        {strings.Replace(child, `"history_mode":"paginated"`, `"history_mode":"paginated","agent_role":"a","agent_type":"b"`, 1), "unsupported_child_metadata"},
	} {
		t.Run(name, func(t *testing.T) { assertHistoryBaseError(t, historyBaseSourceID, parent, tc.header, tc.code) })
	}
	for _, field := range []string{"parent_thread_id", "subagent_history_start_ordinal"} {
		t.Run(field, func(t *testing.T) {
			header := strings.Replace(child, `"history_mode":"paginated"`, `"history_mode":"paginated","`+field+`":0`, 1)
			assertHistoryBaseError(t, historyBaseSourceID, parent, header, "unsupported_child_lineage")
		})
	}
	for name, header := range map[string]string{
		"subagent_source": strings.Replace(child, `"source":"cli"`, `"source":{"subagent":{"thread_spawn":{}}}`, 1),
		"thread_source":   strings.Replace(child, `"source":"cli"`, `"source":"cli","thread_source":"subagent"`, 1),
		"future_source":   strings.Replace(child, `"source":"cli"`, `"source":"future"`, 1),
		"null_source":     strings.Replace(child, `"source":"cli"`, `"source":null`, 1),
		"custom_source":   strings.Replace(child, `"source":"cli"`, `"source":{"custom":"PRIVATE_MARKER"}`, 1),
		"feature_source":  strings.Replace(child, `"source":"cli"`, `"source":"cli","thread_source":"future"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			assertHistoryBaseError(t, historyBaseSourceID, parent, header, "unsupported_child_lineage")
		})
	}
}

func historyBaseHeaderWithoutBase() string {
	meta := strings.ReplaceAll(previewMeta, historyBaseParentThreadID, historyBaseChildID)
	return strings.Replace(meta, `"ordinal":0`, `"ordinal":2`, 1) + "\n"
}

func TestHistoryBasePreviewSeparatesThreadAndRolloutIDs(t *testing.T) {
	parent := previewMeta + "\n"
	child := historyBaseForkFields(historyBaseHeader(1, uint64(len(parent))), 1)
	// Native revert_thread.rs:121,172-189 creates a fresh rollout ID while
	// retaining source_meta.id. Equal logical IDs alone do not imply a cycle.
	sameThread := strings.ReplaceAll(historyBaseHeader(1, uint64(len(parent))), historyBaseChildID, historyBaseParentThreadID)
	if got, err := PreviewAIToolboxHistoryBase(historyBaseSourceID, sha256.Sum256([]byte(parent)), []byte(parent), []byte(sameThread)); err != nil || got.SourceEnd != uint64(len(parent)) {
		t.Fatal("same-thread revision header was confused with a rollout-ID cycle", err)
	}
	for name, tc := range map[string]struct{ header, code string }{
		"logical_id_is_not_rollout_id": {strings.Replace(child, `"forked_from_id":"`+historyBaseParentThreadID, `"forked_from_id":"`+historyBaseSourceID, 1), "unsupported_fork_origin"},
		"wrong_fork_ordinal":           {strings.Replace(child, `"forked_from_ordinal_exclusive":1`, `"forked_from_ordinal_exclusive":2`, 1), "fork_ordinal_mismatch"},
		"missing_fork_id":              {strings.Replace(child, `,"forked_from_id":"`+historyBaseParentThreadID+`"`, "", 1), "unsupported_child_lineage"},
		"missing_fork_ordinal":         {strings.Replace(child, `,"forked_from_ordinal_exclusive":1`, "", 1), "unsupported_child_lineage"},
	} {
		t.Run(name, func(t *testing.T) { assertHistoryBaseError(t, historyBaseSourceID, parent, tc.header, tc.code) })
	}
}

func TestHistoryBasePreviewSourceSubsetAndLimits(t *testing.T) {
	parent := previewMeta + "\n"
	child := historyBaseHeader(1, uint64(len(parent)))
	assertHistoryBaseError(t, historyBaseSourceID, "", child, "source_unsupported_empty")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, "paginated", "legacy", 1), child, "source_unsupported_history")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, `"history_mode":"paginated"`, `"history_mode":"paginated","history_base":{}`, 1), child, "source_unsupported_lineage")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, `"history_mode":"paginated"`, `"history_mode":"paginated","PRIVATE_MARKER":{}`, 1), child, "unsupported_source_metadata")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, `"ordinal":0`, `"ordinal":0,"PRIVATE_MARKER":{}`, 1), child, "unsupported_source_metadata")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, `"source":"cli"`, `"source":"future"`, 1), child, "unsupported_source_lineage")
	assertHistoryBaseError(t, historyBaseSourceID, strings.Replace(parent, `"id":"`+historyBaseParentThreadID+`"`, `"id":"PRIVATE_MARKER"`, 1), child, "unsupported_source_identity")
	lateFailure := parent + `{"ordinal":1,"type":"response_item","payload":` + previewKnownItem(t, "synthetic earlier summary") + "}\n" +
		`{"ordinal":2,"type":"response_item","payload":{"type":"compaction","encrypted_content":"ai-toolbox-compact-v1:PRIVATE_MARKER"}}` + "\n"
	assertHistoryBaseError(t, historyBaseSourceID, lateFailure, child, "source_unreadable_known_compaction")
	for name, alter := range map[string]func(*previewLimits){
		"source_bytes": func(l *previewLimits) { l.bytes = len(parent) - 1 },
		"child_bytes":  func(l *previewLimits) { l.line = len(child) - 1 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := defaultPreviewLimits()
			alter(&limits)
			got, err := previewHistoryBase(historyBaseSourceID, sha256.Sum256([]byte(parent)), []byte(parent), []byte(child), limits)
			var failure *HistoryBasePreviewError
			if !errors.As(err, &failure) || failure.Code != "resource_limit" || !reflect.DeepEqual(got, HistoryBasePreview{}) {
				t.Fatal("resource limit did not fail closed")
			}
		})
	}
	limits := defaultPreviewLimits()
	limits.bytes, limits.line = len(parent), len(child)
	if _, err := previewHistoryBase(historyBaseSourceID, sha256.Sum256([]byte(parent)), []byte(parent), []byte(child), limits); err != nil {
		t.Fatal("exact byte limits should pass", err)
	}
	limits = defaultPreviewLimits()
	limits.depth = 3 // Source metadata fits; the nested history_base exceeds it.
	if got, err := previewHistoryBase(historyBaseSourceID, sha256.Sum256([]byte(parent)), []byte(parent), []byte(child), limits); err == nil || !reflect.DeepEqual(got, HistoryBasePreview{}) {
		t.Fatal("child JSON depth limit did not fail closed")
	}
}

func TestHistoryBasePreviewAcceptsKnownMetadataDefaults(t *testing.T) {
	for name, source := range map[string]string{"absent": "", "vscode": `,"source":"vscode"`, "exec": `,"source":"exec"`, "mcp": `,"source":"mcp"`} {
		t.Run(name, func(t *testing.T) {
			parent := strings.Replace(previewMeta, `,"source":"cli"`, source, 1) + "\n"
			child := strings.Replace(historyBaseHeader(1, uint64(len(parent))), `,"source":"cli"`, source, 1)
			child = strings.Replace(child, `"history_mode":"paginated"`, `"history_mode":"paginated","thread_source":"user","parent_thread_id":null,"subagent_history_start_ordinal":null,"forked_from_id":null,"forked_from_ordinal_exclusive":null`, 1)
			before := []byte(child)
			got, err := PreviewAIToolboxHistoryBase(historyBaseSourceID, sha256.Sum256([]byte(parent)), []byte(parent), before)
			if err != nil || got.SourceEnd != uint64(len(parent)) || got.ProposedEnd != uint64(len(parent)) || string(before) != child {
				t.Fatal("supported native metadata defaults were rejected or changed", err)
			}
		})
	}
}
