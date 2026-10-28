package sessions

import (
	"fmt"
	"sort"
	"strings"
)

// rolloutPlanNode names immutable rollout revisions, not stable thread IDs.
// The caller must obtain canonical IDs from version-matched native metadata
// and filenames. This leaf does not parse IDs or validate a native schema.
type rolloutPlanNode struct {
	rolloutID       string
	parentRolloutID string
}

type rolloutPlanInventory struct {
	// complete is a required caller precondition, not evidence of discovery.
	// The caller must prove that active/archived and compressed/plain inputs
	// were enumerated without unreadable, corrupt or ambiguous omissions.
	complete bool
	nodes    []rolloutPlanNode
	// problems retains traversal, parsing and unknown-format failures even
	// when enumeration reached its end. Duplicates must remain in nodes.
	problems []string
	// Attached relations target stable thread IDs, unlike history_base byte
	// references. This planner does not support them. The caller's native
	// schema parser must detect them; their absence cannot be inferred here.
	hasAttachedRelations bool
}

// orderRolloutPlan orders only history-base byte-reference edges, not every
// native kind of fork relationship. It returns every enumerated revision in deterministic
// parent-before-child order. It performs no I/O, transforms no payload, and
// does not establish reader/writer quiescence or safe graph publication.
// A complete empty inventory is allowed but does not prove a scan succeeded.
func orderRolloutPlan(inventory rolloutPlanInventory) ([]string, error) {
	if !inventory.complete {
		return nil, fmt.Errorf("rollout inventory is incomplete")
	}
	if len(inventory.problems) != 0 {
		return nil, fmt.Errorf("rollout inventory has unresolved problems")
	}
	if inventory.hasAttachedRelations {
		return nil, fmt.Errorf("attached thread relations are not supported")
	}
	// Copy before sorting; input ownership remains with the caller.
	nodes := append([]rolloutPlanNode(nil), inventory.nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].rolloutID < nodes[j].rolloutID })
	var index = make(map[string]int, len(nodes))
	for i, node := range nodes {
		if node.rolloutID == "" || strings.TrimSpace(node.rolloutID) != node.rolloutID || strings.TrimSpace(node.parentRolloutID) != node.parentRolloutID {
			return nil, fmt.Errorf("rollout identity is empty or not canonical")
		}
		if _, exists := index[node.rolloutID]; exists {
			return nil, fmt.Errorf("duplicate rollout identity %q", node.rolloutID)
		}
		index[node.rolloutID] = i
	}
	var children = make([][]int, len(nodes))
	var indegree = make([]int, len(nodes))
	var queue []int
	for i, node := range nodes {
		if node.parentRolloutID == "" {
			queue = append(queue, i)
			continue
		}
		parent, exists := index[node.parentRolloutID]
		if !exists {
			return nil, fmt.Errorf("missing parent rollout %q", node.parentRolloutID)
		}
		children[parent] = append(children[parent], i)
		indegree[i] = 1
	}
	var order = make([]string, 0, len(nodes))
	// Nodes, roots and child lists are sorted by ID. FIFO processing is
	// deterministic without recursion or dependence on map iteration order.
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		order = append(order, nodes[i].rolloutID)
		for _, child := range children[i] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("rollout lineage contains a cycle")
	}
	return order, nil
}
