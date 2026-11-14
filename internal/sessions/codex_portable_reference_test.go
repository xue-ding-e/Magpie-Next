package sessions

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func referenceFixture(t *testing.T) ([]byte, []byte, referenceRewriteInput) {
	t.Helper()
	oldParent := []byte("{\"ordinal\":0,\"payload\":\"short\"}\n")
	newParent := []byte(fmt.Sprintf("{\"ordinal\":0,\"payload\":%q}\n", strings.Repeat("长", 100)))
	plan, err := planRolloutBoundaries(oldParent, newParent)
	if err != nil {
		t.Fatal(err)
	}
	var input = referenceRewriteInput{schemaPin: referenceSchemaPin, parentRolloutID: "parent-revision", oldOffset: uint64(len(oldParent)), exclusiveOrdinal: 1, parentPlan: plan}
	header := func(offset int) []byte {
		return []byte(fmt.Sprintf("{\"ordinal\":1,\"type\":\"session_meta\",\"payload\":{\"history_mode\":\"paginated\",\"history_base\":{\"thread_id\":\"parent-revision\",\"end_ordinal_exclusive\":1,\"end_byte_offset\": %d},\"forked_from_ordinal_exclusive\":9007199254740993,\"opaque\":\"\\uD83D\\uDE00\"}}\r\n{\"ordinal\":2,\"payload\":{\"encrypted_content\":\"native-opaque\",\"call_id\":\"call_fixture\"}}\n", offset))
	}
	return header(len(oldParent)), header(len(newParent)), input
}

func TestReferenceRewriteConserves(t *testing.T) {
	before, after, input := referenceFixture(t)
	oldCopy, newCopy := bytes.Clone(before), bytes.Clone(after)
	plan, err := validateReferenceRewrite(before, after, input)
	if err != nil {
		t.Fatal(err)
	}
	if offset, ok := plan.rebaseCutoff(uint64(len(before)), 3); !ok || offset != uint64(len(after)) {
		t.Fatal("child boundaries not returned")
	}
	if !bytes.Equal(before, oldCopy) || !bytes.Equal(after, newCopy) {
		t.Fatal("input changed")
	}
	// A final header without newline is a supported buffer shape too.
	oldHeader, _ := referenceHeader(before)
	newHeader, _ := referenceHeader(after)
	if _, err := validateReferenceRewrite(bytes.TrimSuffix(oldHeader, []byte("\r\n")), bytes.TrimSuffix(newHeader, []byte("\r\n")), input); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceRewriteRejectsChanges(t *testing.T) {
	before, after, input := referenceFixture(t)
	for name, mutation := range map[string][2]string{
		"wrong-parent": {"parent-revision", "other-revision"}, "stale-ordinal": {"\"end_ordinal_exclusive\":1", "\"end_ordinal_exclusive\":2"},
		"root": {"\"history_base\":{", "\"not_history_base\":{"}, "legacy": {"\"paginated\"", "\"legacy\""},
		"wrong-type": {"\"session_meta\"", "\"response_item\""}, "header-number": {"9007199254740993", "9007199254740992"},
		"opaque-escape": {"\\uD83D\\uDE00", "\\uD801"}, "tail-ciphertext": {"native-opaque", "modified-ciphertext"}, "tail-call": {"call_fixture", "different_call"},
		"whitespace": {"\"end_byte_offset\": ", "\"end_byte_offset\":"}, "terminator": {"\r\n", "\n"},
		"extra-base-field": {"\"thread_id\":", "\"unknown\":0,\"thread_id\":"},
		"attached":         {"\"history_mode\":", "\"attached_thread\":true,\"history_mode\":"},
		"null-attached":    {"\"history_mode\":", "\"attached_thread\":null,\"history_mode\":"},
		"duplicate-key":    {"\"opaque\":", "\"opaque\":0,\"opaque\":"},
	} {
		t.Run(name, func(t *testing.T) {
			changed := bytes.Replace(after, []byte(mutation[0]), []byte(mutation[1]), 1)
			if bytes.Equal(changed, after) {
				t.Fatal("mutation missing")
			}
			if got, err := validateReferenceRewrite(before, changed, input); err == nil || got != nil {
				t.Fatal("accepted changed reference")
			}
		})
	}
	// Parsing must reject duplicates even if raw conservation would pass.
	for _, mutation := range [][2]string{{"\"opaque\":", "\"opaque\":0,\"opaque\":"}, {"\"thread_id\":", "\"thread_id\":\"parent-revision\",\"thread_id\":"}, {"\"ordinal\":1", "\"ordinal\":1,\"ordinal\":1"}, {"\"opaque\":", "\"nested\":{\"k\":1,\"k\":1},\"opaque\":"}} {
		oldBad := bytes.Replace(before, []byte(mutation[0]), []byte(mutation[1]), 1)
		newBad := bytes.Replace(after, []byte(mutation[0]), []byte(mutation[1]), 1)
		if _, err := validateReferenceRewrite(oldBad, newBad, input); err == nil {
			t.Fatal("accepted conserved duplicate keys")
		}
	}
	for _, mutate := range []func(*referenceRewriteInput){func(i *referenceRewriteInput) { i.schemaPin = "unknown" }, func(i *referenceRewriteInput) { i.parentRolloutID = "wrong" }, func(i *referenceRewriteInput) { i.exclusiveOrdinal++ }, func(i *referenceRewriteInput) { i.oldOffset++ }} {
		changed := input
		mutate(&changed)
		if _, err := validateReferenceRewrite(before, after, changed); err == nil {
			t.Fatal("accepted mismatched provenance/cutoff")
		}
	}
}

func TestReferenceRewriteRejectsConservedInvalidShape(t *testing.T) {
	before, after, input := referenceFixture(t)
	for name, mutation := range map[string][2]string{
		"root":               {"\"history_base\":{", "\"not_history_base\":{"},
		"legacy":             {"\"paginated\"", "\"legacy\""},
		"wrongtype":          {"\"session_meta\"", "\"response_item\""},
		"unknown-base-field": {"\"thread_id\":", "\"unknown\":0,\"thread_id\":"},
		"attached-true":      {"\"history_mode\":", "\"attached_thread\":true,\"history_mode\":"},
		"attached-null":      {"\"history_mode\":", "\"attached_thread\":null,\"history_mode\":"},
		"attached-array":     {"\"history_mode\":", "\"attached_thread\":[],\"history_mode\":"},
	} {
		t.Run(name, func(t *testing.T) {
			oldBad := bytes.Replace(before, []byte(mutation[0]), []byte(mutation[1]), 1)
			newBad := bytes.Replace(after, []byte(mutation[0]), []byte(mutation[1]), 1)
			if _, err := validateReferenceRewrite(oldBad, newBad, input); err == nil {
				t.Fatal("accepted conserved invalid shape")
			}
		})
	}
	// Preserve exactly the same non-numeric offset in both buffers.
	replacement, _ := input.parentPlan.rebaseCutoff(input.oldOffset, input.exclusiveOrdinal)
	for _, value := range []string{"null", "\"123\"", "[]", "{}", "1.5", "-1", "18446744073709551616"} {
		oldBad := bytes.Replace(before, []byte(fmt.Sprintf("\"end_byte_offset\": %d", input.oldOffset)), []byte("\"end_byte_offset\": "+value), 1)
		newBad := bytes.Replace(after, []byte(fmt.Sprintf("\"end_byte_offset\": %d", replacement)), []byte("\"end_byte_offset\": "+value), 1)
		if _, err := validateReferenceRewrite(oldBad, newBad, input); err == nil {
			t.Errorf("accepted unsupported offset %s", value)
		}
	}
}

func TestReferenceRewriteResourceSubset(t *testing.T) {
	before, after, input := referenceFixture(t)
	inject := func(data []byte, field string) []byte {
		return bytes.Replace(data, []byte("\"opaque\":"), []byte(field+"\"opaque\":"), 1)
	}
	newHeader, _ := referenceHeader(after)
	paddingSize := (1 << 20) - len(newHeader) - len("\"padding\":\"\",")
	for _, extra := range []int{0, 1} {
		field := "\"padding\":\"" + strings.Repeat("x", paddingSize+extra) + "\","
		oldInput, newInput := inject(before, field), inject(after, field)
		_, err := validateReferenceRewrite(oldInput, newInput, input)
		if (err != nil) != (extra > 0) {
			t.Errorf("size boundary extra %d: %v", extra, err)
		}
	}
	// root depth 0, payload depth 1, nested value depth 2; 62 arrays put
	// their scalar at depth 64. One more array is outside our supported subset.
	for _, count := range []int{62, 63} {
		field := "\"nested\":" + strings.Repeat("[", count) + "0" + strings.Repeat("]", count) + ","
		_, err := validateReferenceRewrite(inject(before, field), inject(after, field), input)
		if (err != nil) != (count > 62) {
			t.Errorf("depth arrays %d: %v", count, err)
		}
	}
}

// This negative test concerns byte conservation only. Isolated surrogates are
// not asserted to be supported by Rust or by native Codex history readers.
func TestReferenceRewriteDoesNotConflateInvalidEscapes(t *testing.T) {
	before, after, input := referenceFixture(t)
	before = bytes.Replace(before, []byte(`\uD83D\uDE00`), []byte(`\uD800`), 1)
	after = bytes.Replace(after, []byte(`\uD83D\uDE00`), []byte(`\uD801`), 1)
	if _, err := validateReferenceRewrite(before, after, input); err == nil {
		t.Fatal("conflated distinct invalid escapes")
	}
}
