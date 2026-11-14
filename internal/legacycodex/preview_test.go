package legacycodex

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const previewMeta = `{"timestamp":"2026-01-01T00:00:00Z","ordinal":0,"type":"session_meta","payload":{"id":"00000000-0000-4000-8000-000000000001","session_id":"00000000-0000-4000-8000-000000000001","timestamp":"2026-01-01T00:00:00Z","cwd":"/synthetic","originator":"codex_cli_rs","cli_version":"0.163.0-alpha.11","source":"cli","model_provider":"synthetic","history_mode":"paginated"}}`

func previewKnownItem(t *testing.T, summary string) string {
	t.Helper()
	return `{"type":"compaction","id":"cmp_synthetic","encrypted_content":"` + synthetic(t, summary) + `"}`
}

// JSON permits literal HTML and U+2028. The existing fixture encoder escapes
// them, which would hide output growth when the proposal's encoder escapes them.
func previewRawItem(t *testing.T, summary string) string {
	t.Helper()
	plain := []byte(`{"summary":"` + summary + `","model":"","created_at":1}`)
	if !json.Valid(plain) {
		t.Fatal("invalid synthetic raw JSON")
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
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return `{"type":"compaction","encrypted_content":"ai-toolbox-compact-v1:` + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)) + `"}`
}

// The expected wire value is assembled independently of the conversion helper
// and span walker, including its role, heading and field order.
func previewExpectedItem(t *testing.T, summary string) string {
	t.Helper()
	text, err := json.Marshal("Context summary from previous turns:\n" + summary)
	if err != nil {
		t.Fatal(err)
	}
	return `{"content":[{"text":` + string(text) + `,"type":"output_text"}],"role":"assistant","type":"message"}`
}

func assertPreview(t *testing.T, original, expected string, changed uint64) RolloutPreview {
	t.Helper()
	input := []byte(original)
	before := bytes.Clone(input)
	got, err := PreviewAIToolboxRollout(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, before) {
		t.Fatal("preview mutated source bytes")
	}
	if got.Version != 1 || got.SourceSHA256 != sha256.Sum256(before) || got.ProposedSHA256 != sha256.Sum256([]byte(expected)) ||
		got.SourceBytes != uint64(len(original)) || got.ProposedBytes != uint64(len(expected)) || got.ConvertedItems != changed {
		t.Fatal("preview hash, byte length or conversion count differs from independent expected bytes")
	}
	oldLines := strings.SplitAfter(original, "\n")
	newLines := strings.SplitAfter(expected, "\n")
	if len(got.Boundaries) != len(oldLines)-1 || len(oldLines) != len(newLines) {
		t.Fatal("record count changed")
	}
	var oldOffset, newOffset uint64
	for i, boundary := range got.Boundaries {
		want := RolloutBoundary{Ordinal: uint64(i), SourceStart: oldOffset, SourceEnd: oldOffset + uint64(len(oldLines[i])),
			ProposedStart: newOffset, ProposedEnd: newOffset + uint64(len(newLines[i]))}
		if boundary != want {
			t.Fatalf("record %d decoded byte boundary differs", i)
		}
		mapped, ok := got.ProposedCutoff(want.SourceEnd, uint64(i+1))
		if !ok || mapped != want.ProposedEnd {
			t.Fatal("exact ordinal and byte cutoff did not map")
		}
		if _, ok := got.ProposedCutoff(want.SourceEnd-1, uint64(i+1)); ok {
			t.Fatal("mid-record cutoff was accepted")
		}
		if _, ok := got.ProposedCutoff(want.SourceEnd, uint64(i+2)); ok {
			t.Fatal("wrong exclusive ordinal was accepted")
		}
		oldOffset, newOffset = want.SourceEnd, want.ProposedEnd
	}
	if _, ok := got.ProposedCutoff(0, 0); ok {
		t.Fatal("zero cutoff was accepted")
	}
	return got
}

func TestPreviewExactBytesAndBoundaries(t *testing.T) {
	for name, summary := range map[string]string{
		"short": "S", "long_shrinks": strings.Repeat("Synthetic summary. ", 100),
		"raw_html_grows": strings.Repeat("<>&\u2028", 30), "equal_length": strings.Repeat("<>&\u2028", 30),
		"unicode_and_whitespace": " \n合成 😀\t<&> ",
	} {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(name+fmt.Sprint(len(ending)), func(t *testing.T) {
				item := previewKnownItem(t, summary)
				if name == "raw_html_grows" || name == "equal_length" {
					item = previewRawItem(t, summary)
				}
				if name == "equal_length" {
					padding := len(previewExpectedItem(t, summary)) - len(item)
					if padding < 0 {
						t.Fatal("equal-length fixture cannot be padded")
					}
					item = strings.TrimSuffix(item, "}") + strings.Repeat(" ", padding) + "}"
				}
				line := `{ "ordinal" : 1, "type" : "response_item", "unknown" : {"huge":900719925474099312345678901234567890, "exp":1e100000, "negative_zero":-0, "text":"\\uD800"}, "payload" : ` + item + `, "after" : [true,null, "😀"] }` + ending
				original := previewMeta + ending + line
				expected := previewMeta + ending + strings.Replace(line, item, previewExpectedItem(t, summary), 1)
				got := assertPreview(t, original, expected, 1)
				if name == "raw_html_grows" && got.ProposedBytes <= got.SourceBytes {
					t.Fatal("fixture did not exercise a growing record")
				}
				if name == "long_shrinks" && got.ProposedBytes >= got.SourceBytes {
					t.Fatal("fixture did not exercise a shrinking record")
				}
				if name == "equal_length" && got.ProposedBytes != got.SourceBytes {
					t.Fatal("fixture did not exercise an equal-byte-length replacement")
				}
				assertPreview(t, expected, expected, 0)
			})
		}
	}
}

func TestPreviewCheckpointPreservesNonTargets(t *testing.T) {
	summary := "SYNTHETIC_PRIVATE_SUMMARY_94A"
	item := previewKnownItem(t, summary)
	other := `{"type":"reasoning","id":"reasoning_0","encrypted_content":"` + synthetic(t, summary) + `","summary":[{"type":"summary_text","text":"keep exactly"}]}`
	tools := `{"type":"function_call","call_id":"call_synthetic","arguments":"{\"type\":\"compaction\"}"}, {"type":"function_call_output","call_id":"call_synthetic","output":"synthetic"}`
	line := ` {"ordinal":1,"type":"compacted","payload":{"message":"synthetic","replacement_history" : [` + item + ` , ` + other + `, ` + tools + `,` + item + `], "replacement_history_metadata":[{},{"unknown":9007199254740993},{},{},{"source":"unchanged"}],"nested":{"do_not_expand":` + item + `}}} ` + "\r\n"
	expected := strings.Replace(line, item, previewExpectedItem(t, summary), 1)
	last := strings.LastIndex(expected, ","+item+"]")
	if last < 0 {
		t.Fatal("invalid synthetic expected fixture")
	}
	expected = expected[:last+1] + previewExpectedItem(t, summary) + expected[last+1+len(item):]
	got := assertPreview(t, previewMeta+"\n"+line, previewMeta+"\n"+expected, 2)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(encoded), fmt.Sprint(got), fmt.Sprintf("%+v", got), fmt.Sprintf("%#v", got)} {
		for _, marker := range []string{summary, "call_synthetic", "reasoning_0", "/synthetic", synthetic(t, summary)} {
			if strings.Contains(output, marker) {
				t.Fatal("report exposes content or identity")
			}
		}
	}
}

func TestPreviewMultipleRecordOffsets(t *testing.T) {
	growingSummary := strings.Repeat("<>&\u2028", 30)
	shrinkingSummary := strings.Repeat("Synthetic summary. ", 100)
	growing := previewRawItem(t, growingSummary)
	shrinking := previewKnownItem(t, shrinkingSummary)
	if len(previewExpectedItem(t, growingSummary)) <= len(growing) || len(previewExpectedItem(t, shrinkingSummary)) >= len(shrinking) {
		t.Fatal("fixtures must exercise growth followed by shrinkage")
	}
	lines := []string{
		previewMeta + "\n",
		` {"ordinal":1,"type":"response_item","payload":` + growing + `} ` + "\r\n",
		`{"ordinal":2,"type":"response_item","payload":{"type":"function_call","call_id":"call_synthetic","name":"synthetic","arguments":"{}"}}` + "\n",
		`{"ordinal":3,"type":"response_item","payload":` + shrinking + `}` + "\r\n",
		`{"ordinal":4,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_synthetic","output":"合成 😀"}}` + "\n",
	}
	original := strings.Join(lines, "")
	lines[1] = strings.Replace(lines[1], growing, previewExpectedItem(t, growingSummary), 1)
	lines[3] = strings.Replace(lines[3], shrinking, previewExpectedItem(t, shrinkingSummary), 1)
	assertPreview(t, original, strings.Join(lines, ""), 2)
}

func TestPreviewRejectsAfterEarlierConversion(t *testing.T) {
	prefix := previewMeta + "\n" + `{"ordinal":1,"type":"response_item","payload":` + previewKnownItem(t, "synthetic earlier summary") + "}\n" +
		`{"ordinal":2,"type":"event_msg","payload":{"type":"synthetic_untouched"}}` + "\n"
	for name, test := range map[string]struct{ line, code string }{
		"malformed_json": {`{"ordinal":3,"type":"event_msg","payload":` + "\n", "invalid_json"},
		"duplicate_key":  {`{"ordinal":3,"type":"event_msg","payload":{"PRIVATE_MARKER":1,"PRIVATE_MARKER":2}}` + "\n", "duplicate_key"},
		"bad_capsule":    {`{"ordinal":3,"type":"response_item","payload":{"type":"compaction","encrypted_content":"ai-toolbox-compact-v1:PRIVATE_MARKER"}}` + "\n", "unreadable_known_compaction"},
		"wrong_ordinal":  {`{"ordinal":4,"type":"event_msg","payload":{}}` + "\n", "unsupported_ordinal_sequence"},
	} {
		t.Run(name, func(t *testing.T) {
			assertPreviewError(t, prefix+test.line, test.code)
		})
	}
}

func TestPreviewUntouchedItems(t *testing.T) {
	valid := synthetic(t, "synthetic")
	for name, item := range map[string]string{
		"native_reasoning":  `{"type":"reasoning","id":"rs_synthetic","encrypted_content":"OPAQUE_SYNTHETIC","summary":[]}`,
		"legacy_reasoning":  `{"type":"reasoning","id":"reasoning_0","encrypted_content":"` + valid + `"}`,
		"native_compaction": `{"type":"compaction","encrypted_content":"OPAQUE_SYNTHETIC"}`,
		"unknown_capsule":   `{"type":"compaction","encrypted_content":"unknown:synthetic"}`,
		"unproven_alias":    `{"type":"compaction_summary","encrypted_content":"` + valid + `"}`,
		"text_marker":       `{"type":"message","role":"user","content":[{"type":"input_text","text":"ai-toolbox-compact-v1:broken"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			data := previewMeta + "\n" + `{"ordinal":1,"type":"response_item","payload":` + item + "}\n"
			assertPreview(t, data, data, 0)
		})
	}
	for _, kind := range []string{"event_msg", "turn_context", "unknown_future_record"} {
		t.Run("untouched_location_"+kind, func(t *testing.T) {
			data := previewMeta + "\n" + `{"ordinal":1,"type":"` + kind + `","payload":` + previewKnownItem(t, "synthetic") + "}\n"
			assertPreview(t, data, data, 0)
		})
	}
	for name, suffix := range map[string]string{"missing": "", "null": `,"history_base":null,"subagent_history_start_ordinal":null,"parent_thread_id":null,"forked_from_id":null,"forked_from_ordinal_exclusive":null`} {
		t.Run("lineage_"+name, func(t *testing.T) {
			meta := strings.TrimSuffix(previewMeta, "}}") + suffix + "}}\n"
			assertPreview(t, meta, meta, 0)
		})
	}
}

func assertPreviewError(t *testing.T, data, code string) {
	t.Helper()
	input := []byte(data)
	before := bytes.Clone(input)
	got, err := PreviewAIToolboxRollout(input)
	var failure *PreviewError
	if !errors.As(err, &failure) || failure.Code != code || !reflect.DeepEqual(got, RolloutPreview{}) || !bytes.Equal(input, before) {
		t.Fatalf("expected zero report and %s, got report=%+v, error=%v", code, got, err)
	}
	if strings.Contains(err.Error(), "PRIVATE_MARKER") || strings.Contains(fmt.Sprintf("%#v", err), "PRIVATE_MARKER") {
		t.Fatal("error exposes input")
	}
}

func TestPreviewRejectsAmbiguousOrUnsupportedInput(t *testing.T) {
	valid := previewMeta + "\n"
	for name, test := range map[string]struct{ data, code string }{
		"empty":              {"", "unsupported_empty"},
		"tail":               {previewMeta, "incomplete_tail"},
		"blank":              {valid + "\n", "invalid_json"},
		"two_values":         {previewMeta + " {}\n", "invalid_json"},
		"array_root":         {"[]\n", "unsupported_record"},
		"missing_type":       {strings.Replace(valid, `"type":"session_meta",`, "", 1), "unsupported_record"},
		"missing_payload":    {"{\"type\":\"session_meta\",\"ordinal\":0}\n", "unsupported_record"},
		"legacy":             {strings.Replace(valid, "paginated", "legacy", 1), "unsupported_history"},
		"missing_mode":       {strings.Replace(valid, `,"history_mode":"paginated"`, "", 1), "unsupported_history"},
		"copied_meta":        {valid + strings.Replace(valid, `"ordinal":0`, `"ordinal":1`, 1), "unsupported_copied_metadata"},
		"duplicate_key":      {strings.Replace(valid, `"ordinal":0`, `"ordinal":0,"ordinal":0`, 1), "duplicate_key"},
		"escaped_key":        {strings.Replace(valid, `"ordinal":0`, `"ordinal":0,"\u006frdinal":0`, 1), "duplicate_key"},
		"unknown_duplicate":  {strings.Replace(valid, `"ordinal":0`, `"ordinal":0,"PRIVATE_MARKER":{"x":1,"x":2}`, 1), "duplicate_key"},
		"bad_utf8":           {strings.Replace(valid, "synthetic", "\xff", 1), "invalid_json"},
		"high_surrogate":     {strings.Replace(valid, "synthetic", `\uD800`, 1), "invalid_json"},
		"low_surrogate":      {strings.Replace(valid, "synthetic", `\uDC00`, 1), "invalid_json"},
		"unpaired_surrogate": {strings.Replace(valid, "synthetic", `\uD800\u0041`, 1), "invalid_json"},
		"bad_item":           {valid + "{\"ordinal\":1,\"type\":\"response_item\",\"payload\":null}\n", "unsupported_record"},
	} {
		t.Run(name, func(t *testing.T) { assertPreviewError(t, test.data, test.code) })
	}
	for _, ordinal := range []string{"null", "-0", "-1", "0.0", "0e0", "1", "18446744073709551616"} {
		t.Run("ordinal_"+ordinal, func(t *testing.T) {
			assertPreviewError(t, strings.Replace(valid, `"ordinal":0`, `"ordinal":`+ordinal, 1), "unsupported_ordinal_sequence")
		})
	}
	for _, ordinal := range []string{"0", "2", "18446744073709551615"} {
		t.Run("later_ordinal_"+ordinal, func(t *testing.T) {
			assertPreviewError(t, valid+`{"ordinal":`+ordinal+`,"type":"event_msg","payload":{}}`+"\n", "unsupported_ordinal_sequence")
		})
	}
	t.Run("missing_ordinal", func(t *testing.T) {
		assertPreviewError(t, strings.Replace(valid, `"ordinal":0,`, "", 1), "unsupported_ordinal_sequence")
	})
	for _, field := range []string{"history_base", "subagent_history_start_ordinal", "parent_thread_id", "forked_from_id", "forked_from_ordinal_exclusive"} {
		t.Run(field, func(t *testing.T) {
			data := strings.TrimSuffix(previewMeta, "}}") + `,"` + field + `":0}}` + "\n"
			assertPreviewError(t, data, "unsupported_lineage")
		})
	}
	t.Run("subagent_source", func(t *testing.T) {
		data := strings.Replace(valid, `"source":"cli"`, `"source":{"subagent":{"thread_spawn":{"parent_thread_id":"00000000-0000-4000-8000-000000000002","depth":1}}}`, 1)
		assertPreviewError(t, data, "unsupported_lineage")
	})
	t.Run("subagent_thread_source", func(t *testing.T) {
		data := strings.TrimSuffix(previewMeta, "}}") + `,"thread_source":"subagent"}}` + "\n"
		assertPreviewError(t, data, "unsupported_lineage")
	})
	paired := strings.Replace(valid, "synthetic", `\uD83D\uDE00`, 1)
	assertPreview(t, paired, paired, 0)
}

func TestPreviewRejectsWholeProposalOnKnownItemFailure(t *testing.T) {
	good := previewKnownItem(t, "synthetic good")
	for name, item := range map[string]string{
		"bad_capsule":   `{"type":"compaction","encrypted_content":"ai-toolbox-compact-v1:PRIVATE_MARKER"}`,
		"unknown_field": strings.TrimSuffix(good, "}") + `,"PRIVATE_MARKER":9007199254740993}`,
		"wrong_id_type": strings.Replace(good, `"cmp_synthetic"`, "7", 1),
		"blank_summary": previewKnownItem(t, " \n\t "),
	} {
		t.Run(name, func(t *testing.T) {
			data := previewMeta + "\n" + `{"ordinal":1,"type":"compacted","payload":{"message":"synthetic","replacement_history":[` + good + `,` + item + "]}}\n"
			assertPreviewError(t, data, "unreadable_known_compaction")
		})
	}
	for name, metadata := range map[string]string{"short": "[]", "long": "[{},{}]", "null_element": "[null]", "object": "{}", "string": `"PRIVATE_MARKER"`} {
		t.Run("metadata_"+name, func(t *testing.T) {
			data := previewMeta + "\n" + `{"ordinal":1,"type":"compacted","payload":{"message":"synthetic","replacement_history":[` + good + `],"replacement_history_metadata":` + metadata + "}}\n"
			assertPreviewError(t, data, "unsupported_metadata_alignment")
		})
	}
	for _, history := range []string{"", `,"replacement_history":null`} {
		data := previewMeta + "\n" + `{"ordinal":1,"type":"compacted","payload":{"message":"synthetic","replacement_history_metadata":[]` + history + "}}\n"
		assertPreviewError(t, data, "unsupported_metadata_alignment")
	}
}

func TestPreviewResourceBounds(t *testing.T) {
	input := []byte(previewMeta + "\n")
	for name, alter := range map[string]func(*previewLimits){
		"input_bytes":  func(l *previewLimits) { l.bytes = len(input) - 1 },
		"record_bytes": func(l *previewLimits) { l.line = len(input) - 1 },
		"record_count": func(l *previewLimits) { l.records = 0 },
		"depth":        func(l *previewLimits) { l.depth = 1 },
		"values":       func(l *previewLimits) { l.values = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := defaultPreviewLimits()
			alter(&limits)
			got, err := previewRollout(input, limits)
			var failure *PreviewError
			if !errors.As(err, &failure) || failure.Code != "resource_limit" || !reflect.DeepEqual(got, RolloutPreview{}) {
				t.Fatal("resource limit did not fail with a zero report")
			}
		})
	}
	limits := defaultPreviewLimits()
	limits.bytes, limits.line, limits.records = len(input), len(input), 1
	if _, err := previewRollout(input, limits); err != nil {
		t.Fatal("exact limits should be accepted", err)
	}
	item := previewRawItem(t, strings.Repeat("<", 100))
	growing := []byte(previewMeta + "\n" + `{"ordinal":1,"type":"response_item","payload":` + item + "}\n")
	limits = defaultPreviewLimits()
	limits.bytes = len(growing)
	if got, err := previewRollout(growing, limits); err == nil || !reflect.DeepEqual(got, RolloutPreview{}) {
		t.Fatal("proposed byte limit did not reject growth")
	}
	limits = defaultPreviewLimits()
	limits.line = max(len(previewMeta)+1, len(growing)-len(previewMeta)-1)
	if got, err := previewRollout(growing, limits); err == nil || !reflect.DeepEqual(got, RolloutPreview{}) {
		t.Fatal("proposed record limit did not reject growth")
	}
}
