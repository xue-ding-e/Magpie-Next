package sessions

import (
	"fmt"
	"reflect"
	"testing"
)

func TestRolloutGraphOrder(t *testing.T) {
	var nodes = []rolloutPlanNode{{"grandchild", "child"}, {"sibling", "root"}, {"child", "root"}, {"other", ""}, {"root", ""}}
	original := append([]rolloutPlanNode(nil), nodes...)
	var want = []string{"other", "root", "child", "sibling", "grandchild"}
	for rotation := 0; rotation < len(nodes); rotation++ {
		shuffled := append(append([]rolloutPlanNode(nil), nodes[rotation:]...), nodes[:rotation]...)
		got, err := orderRolloutPlan(rolloutPlanInventory{complete: true, nodes: shuffled})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("rotation %d: got %v, %v; want %v", rotation, got, err, want)
		}
		for i, node := range shuffled {
			if node != original[(rotation+i)%len(original)] {
				t.Fatal("mutated input")
			}
		}
	}
}

func TestRolloutGraphRejects(t *testing.T) {
	for name, nodes := range map[string][]rolloutPlanNode{
		"empty-id": {{"", ""}}, "space-id": {{" root", ""}}, "space-parent": {{"child", "root "}, {"root", ""}},
		"identical-duplicate":   {{"root", ""}, {"root", ""}},
		"conflicting-duplicate": {{"root", ""}, {"root", "other"}, {"other", ""}},
		"missing":               {{"child", "absent"}}, "self": {{"self", "self"}},
		"cycle":              {{"a", "b"}, {"b", "c"}, {"c", "a"}},
		"disconnected-cycle": {{"root", ""}, {"child", "root"}, {"a", "b"}, {"b", "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := orderRolloutPlan(rolloutPlanInventory{complete: true, nodes: nodes}); err == nil || got != nil {
				t.Errorf("accepted invalid graph: %v, %v", got, err)
			}
		})
	}
	for _, inventory := range []rolloutPlanInventory{{}, {nodes: []rolloutPlanNode{{"root", ""}}}} {
		if got, err := orderRolloutPlan(inventory); err == nil || got != nil {
			t.Fatal("accepted incomplete inventory")
		}
	}
	for _, inventory := range []rolloutPlanInventory{
		{complete: true, problems: []string{"parse failure"}},
		{complete: true, problems: []string{""}},
		{complete: true, hasAttachedRelations: true},
		{complete: true, nodes: []rolloutPlanNode{{"root", ""}}, problems: []string{"unknown format"}},
		{complete: true, nodes: []rolloutPlanNode{{"root", ""}}, hasAttachedRelations: true},
	} {
		if got, err := orderRolloutPlan(inventory); err == nil || got != nil {
			t.Fatal("accepted unsupported or failed inventory")
		}
	}
	if got, err := orderRolloutPlan(rolloutPlanInventory{complete: true}); err != nil || len(got) != 0 {
		t.Fatalf("complete empty inventory: %v, %v", got, err)
	}
}

func TestRolloutGraphDeepChain(t *testing.T) {
	const count = 10000
	var nodes = make([]rolloutPlanNode, count)
	for i := 0; i < count; i++ {
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("r%05d", i-1)
		}
		nodes[count-i-1] = rolloutPlanNode{rolloutID: fmt.Sprintf("r%05d", i), parentRolloutID: parent}
	}
	got, err := orderRolloutPlan(rolloutPlanInventory{complete: true, nodes: nodes})
	if err != nil || len(got) != count {
		t.Fatalf("chain: count %d, err %v", len(got), err)
	}
	for i, id := range got {
		if id != fmt.Sprintf("r%05d", i) {
			t.Fatalf("out of order at %d: %s", i, id)
		}
	}
}
