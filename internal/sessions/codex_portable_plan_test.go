package sessions

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRolloutPlanOrdersBoundaryRewrites(t *testing.T) {
	// This is a synthetic in-memory composition, not a native history parser,
	// projection rebuild, or publication protocol. The complete flag is a
	// fixture assertion; real discovery must independently establish it.
	var inventory = rolloutPlanInventory{complete: true, nodes: []rolloutPlanNode{
		{"grandchild-revision", "child-revision"}, {"child-revision", "parent-revision"}, {"parent-revision", ""},
	}}
	parentOld := []byte("{\"ordinal\":0,\"payload\":\"short\"}\n")
	parentNew := []byte(fmt.Sprintf("{\"ordinal\":0,\"payload\":%q}\n", strings.Repeat("长", 400)))
	header := func(ordinal uint64, parent string, end uint64) []byte {
		return []byte(fmt.Sprintf("{\"ordinal\":%d,\"type\":\"session_meta\",\"payload\":{\"history_base\":{\"thread_id\":%q,\"end_ordinal_exclusive\":%d,\"end_byte_offset\":%d}}}\n", ordinal, parent, ordinal, end))
	}
	childTail := []byte("{\"ordinal\":2,\"payload\":\"unchanged child\"}\n")
	childOld := append(header(1, "parent-revision", uint64(len(parentOld))), childTail...)
	grandchildOld := header(3, "child-revision", uint64(len(childOld)))
	var old = map[string][]byte{"parent-revision": parentOld, "child-revision": childOld, "grandchild-revision": grandchildOld}
	var staged = make(map[string][]byte)
	var plans = make(map[string]boundaryPlan)
	order, err := orderRolloutPlan(inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range order {
		var next []byte
		switch id {
		case "parent-revision":
			next = parentNew
		case "child-revision":
			plan, ready := plans["parent-revision"]
			if !ready {
				t.Fatal("child visited before parent boundary map")
			}
			cutoff, ok := plan.rebaseCutoff(uint64(len(parentOld)), 1)
			if !ok {
				t.Fatal("parent cutoff not mapped")
			}
			next = append(header(1, "parent-revision", cutoff), childTail...)
		case "grandchild-revision":
			plan, ready := plans["child-revision"]
			if !ready {
				t.Fatal("grandchild visited before child boundary map")
			}
			cutoff, ok := plan.rebaseCutoff(uint64(len(childOld)), 3)
			if !ok || cutoff != uint64(len(staged["child-revision"])) {
				t.Fatal("grandchild cutoff does not address staged child")
			}
			next = header(3, "child-revision", cutoff)
		}
		plan, err := planRolloutBoundaries(old[id], next)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		staged[id], plans[id] = next, plan
	}
	if len(plans) != 3 || bytes.Equal(staged["grandchild-revision"], grandchildOld) {
		t.Fatal("descendant rewrite was skipped")
	}
	if len(staged["child-revision"])-len(childOld) == len(parentNew)-len(parentOld) {
		t.Fatal("fixture does not distinguish parent and child byte deltas")
	}
}
