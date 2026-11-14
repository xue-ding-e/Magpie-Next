package sessions

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRolloutBoundaryPlanBytes(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", ""} {
		for _, text := range []string{"ab", "短", "🙂", "much longer replacement", "abc"} {
			t.Run(fmt.Sprintf("%q/%s", ending, text), func(t *testing.T) {
				beforeFirst := []byte("{\"ordinal\":9,\"payload\":\"abc\"}\r\n")
				afterFirst := []byte(fmt.Sprintf("{\"ordinal\":9,\"payload\":%q}\r\n", text))
				tail := []byte("{\"ordinal\":10,\"payload\":{\"call_id\":\"call_fixture\",\"encrypted_content\":\"opaque\"}}" + ending)
				before := append(bytes.Clone(beforeFirst), tail...)
				after := append(bytes.Clone(afterFirst), tail...)
				originalBefore, originalAfter := bytes.Clone(before), bytes.Clone(after)
				plan, err := planRolloutBoundaries(before, after)
				if err != nil {
					t.Fatal(err)
				}
				for old, want := range map[uint64]uint64{0: 0, uint64(len(beforeFirst)): uint64(len(afterFirst)), uint64(len(before)): uint64(len(after))} {
					if got, ok := plan.rebaseBoundary(old); !ok || got != want {
						t.Errorf("boundary %d = %d,%v; want %d", old, got, ok, want)
					}
				}
				for _, offset := range []uint64{1, uint64(len(beforeFirst) - 1), uint64(len(before) + 1)} {
					if _, ok := plan.rebaseBoundary(offset); ok {
						t.Errorf("accepted non-boundary %d", offset)
					}
				}
				if !bytes.Equal(before, originalBefore) || !bytes.Equal(after, originalAfter) {
					t.Fatal("mutated input")
				}
			})
		}
	}
}

func TestRolloutBoundaryPlanDescendant(t *testing.T) {
	// Rewriting a parent's summary moves its cutoff. Re-encoding that cutoff
	// changes the child's header length, so a grandchild needs the child's own
	// byte map, not the parent's delta. All buffers are synthetic and in-memory.
	parentOld := []byte("{\"ordinal\":0,\"payload\":\"short summary\"}\n")
	parentNew := []byte(fmt.Sprintf("{\"ordinal\":0,\"payload\":%q}\n", strings.Repeat("长", 400)))
	parent, err := planRolloutBoundaries(parentOld, parentNew)
	if err != nil {
		t.Fatal(err)
	}
	cutoff, ok := parent.rebaseCutoff(uint64(len(parentOld)), 1)
	if !ok {
		t.Fatal("parent cutoff absent")
	}
	child := func(end uint64) []byte {
		return []byte(fmt.Sprintf("{\"ordinal\":1,\"type\":\"session_meta\",\"payload\":{\"history_base\":{\"end_ordinal_exclusive\":1,\"end_byte_offset\":%d}}}\n{\"ordinal\":2,\"payload\":\"unchanged child\"}\n", end))
	}
	childOld, childNew := child(uint64(len(parentOld))), child(cutoff)
	childPlan, err := planRolloutBoundaries(childOld, childNew)
	if err != nil {
		t.Fatal(err)
	}
	grandchildCutoff, ok := childPlan.rebaseCutoff(uint64(len(childOld)), 3)
	if !ok || grandchildCutoff != uint64(len(childNew)) {
		t.Fatal("grandchild cutoff not rebased to child bytes")
	}
	if len(childNew)-len(childOld) == len(parentNew)-len(parentOld) {
		t.Fatal("fixture failed to distinguish per-generation delta")
	}
}

func TestRolloutBoundaryPlanCutoffPair(t *testing.T) {
	first := "{\"ordinal\":9}\n"
	data := []byte(first + "{\"ordinal\":10}\n")
	plan, err := planRolloutBoundaries(data, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]uint64{{uint64(len(first)), 10}, {uint64(len(data)), 11}} {
		if offset, ok := plan.rebaseCutoff(pair[0], pair[1]); !ok || offset != pair[0] {
			t.Errorf("valid pair %v rejected", pair)
		}
	}
	for _, pair := range [][2]uint64{{uint64(len(first)), 9}, {uint64(len(first)), 11}, {uint64(len(data)), 10}, {uint64(len(data)), 0}, {uint64(len(data)), ^uint64(0)}, {0, 9}, {0, 0}, {1, 10}, {uint64(len(data) + 1), 11}} {
		if _, ok := plan.rebaseCutoff(pair[0], pair[1]); ok {
			t.Errorf("accepted stale or invalid cutoff pair %v", pair)
		}
	}
	// There is no representable exclusive ordinal after this record.
	max := []byte("{\"ordinal\":18446744073709551615}\n")
	if _, err := planRolloutBoundaries(max, max); err == nil {
		t.Fatal("accepted ordinal overflow")
	}
}

func TestRolloutBoundaryPlanRejectsInvalidIdentity(t *testing.T) {
	for _, value := range []string{"", "\n", "{}", "null", "[]", "{\"Ordinal\":0}", "{\"ordinal\":null}", "{\"ordinal\":0,\"ordinal\":0}", "{\"ordinal\":0}\n{\"ordinal\":2}", "{\"ordinal\":18446744073709551615}"} {
		if plan, err := planRolloutBoundaries([]byte(value), []byte(value)); err == nil || plan != nil {
			t.Errorf("accepted invalid identical records %q", value)
		}
	}
}

func TestRolloutBoundaryPlanRejects(t *testing.T) {
	good := "{\"ordinal\":9}\n{\"ordinal\":10}\n"
	for name, invalid := range map[string]string{
		"missing": "{}\n", "null": "{\"ordinal\":null}\n", "string": "{\"ordinal\":\"9\"}\n",
		"fraction": "{\"ordinal\":9.0}\n", "negative": "{\"ordinal\":-1}\n", "array": "[]\n",
		"malformed": "{", "blank": "{\"ordinal\":9}\n\n", "empty": "",
		"count": "{\"ordinal\":9}\n", "gap": "{\"ordinal\":9}\n{\"ordinal\":11}\n",
		"reorder": "{\"ordinal\":10}\n{\"ordinal\":9}\n", "duplicate": "{\"ordinal\":9,\"ordinal\":9}\n{\"ordinal\":10}\n",
		"max": "{\"ordinal\":18446744073709551615}\n", "invalidUTF8": "{\"ordinal\":9,\"s\":\"\xff\"}\n{\"ordinal\":10}\n",
	} {
		t.Run(name, func(t *testing.T) {
			pairs := [][2]string{{good, invalid}, {invalid, good}}
			if name != "count" {
				pairs = append(pairs, [2]string{invalid, invalid})
			}
			for _, pair := range pairs {
				if p, err := planRolloutBoundaries([]byte(pair[0]), []byte(pair[1])); err == nil || p != nil {
					t.Errorf("accepted invalid %s input", name)
				}
			}
		})
	}
}
