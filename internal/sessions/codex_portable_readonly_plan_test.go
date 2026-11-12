package sessions

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const stagedThread = "11111111-1111-4111-8111-111111111111"
const stagedSession = "66666666-6666-4666-8666-666666666666"

const stagedRootA = "22222222-2222-4222-8222-222222222222"
const stagedRootB = "33333333-3333-4333-8333-333333333333"
const stagedChild = "44444444-4444-4444-8444-444444444444"
const stagedGrandchild = "55555555-5555-4555-8555-555555555555"

func stagedFixture(t *testing.T) stagedRolloutInventory {
	t.Helper()
	name := func(thread, id string) string {
		if thread == id {
			return "rollout-2026-10-26T01-02-03-" + thread + ".jsonl"
		}
		return "rollout-2026-10-26T01-02-03-" + thread + "_" + id + ".jsonl"
	}
	header := func(thread, parent string, ordinal, offset uint64) string {
		base := ""
		if parent != "" {
			base = fmt.Sprintf(",\"history_base\":{\"thread_id\":%q,\"end_ordinal_exclusive\":%d,\"end_byte_offset\":%d}", parent, ordinal, offset)
		}
		return fmt.Sprintf("{\"ordinal\":%d,\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"session_id\":%q,\"history_mode\":\"paginated\"%s}}\n", ordinal, thread, stagedSession, base)
	}
	rootOld := []byte(header(stagedThread, "", 0, 0) + "{\"ordinal\":1,\"payload\":\"short\"}\n")
	rootNew := []byte(header(stagedThread, "", 0, 0) + fmt.Sprintf("{\"ordinal\":1,\"payload\":%q}\n", strings.Repeat("长", 400)))
	childTail := "{\"ordinal\":3,\"payload\":{\"call_id\":\"call_fixture\",\"encrypted_content\":\"native-opaque\"}}\n"
	childOld := []byte(header(stagedChild, stagedRootB, 2, uint64(len(rootOld))) + childTail)
	childNew := []byte(header(stagedChild, stagedRootB, 2, uint64(len(rootNew))) + childTail)
	grandOld := []byte(header(stagedGrandchild, stagedChild, 4, uint64(len(childOld))))
	grandNew := []byte(header(stagedGrandchild, stagedChild, 4, uint64(len(childNew))))
	return stagedRolloutInventory{schemaPin: referenceSchemaPin, complete: true, rollouts: []stagedRolloutBytes{
		{name(stagedGrandchild, stagedGrandchild), stagedGrandchild, grandOld, grandNew},
		{name(stagedChild, stagedChild), stagedChild, childOld, childNew},
		{name(stagedThread, stagedRootB), stagedRootB, bytes.Clone(rootOld), rootNew},
		{name(stagedThread, stagedRootA), stagedRootA, bytes.Clone(rootOld), bytes.Clone(rootOld)},
	}}
}

func TestStagedReferencePlanBindsImmutableRevisions(t *testing.T) {
	inventory := stagedFixture(t)
	result, err := validateStagedRolloutReferences(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range result.entries {
		ids = append(ids, entry.rolloutID)
	}
	if !reflect.DeepEqual(ids, []string{stagedRootA, stagedRootB, stagedChild, stagedGrandchild}) {
		t.Fatalf("unexpected order: %v", ids)
	}
	if !reflect.DeepEqual(result.changedRootPayloads, []string{stagedRootB}) {
		t.Fatal("unreviewed root transformation not reported")
	}
	saved := result
	saved.entries = append([]readOnlyRolloutSummary(nil), result.entries...)
	saved.changedRootPayloads = append([]string(nil), result.changedRootPayloads...)
	for _, item := range inventory.rollouts {
		for i := range item.before {
			item.before[i] = 'x'
		}
		for i := range item.after {
			item.after[i] = 'y'
		}
	}
	if !reflect.DeepEqual(result, saved) {
		t.Fatal("result retained mutable input bytes")
	}
}

func TestStagedReferencePlanRejectsWrongParentMap(t *testing.T) {
	inventory := stagedFixture(t)
	// Both roots have identical old cutoff bytes/ordinals and the same stable
	// thread ID, but only revision B grows. Wiring A's unchanged map to B would
	// wrongly accept these stale descendant candidates.
	inventory.rollouts[0].after = bytes.Clone(inventory.rollouts[0].before)
	inventory.rollouts[1].after = bytes.Clone(inventory.rollouts[1].before)
	if result, err := validateStagedRolloutReferences(inventory); err == nil || len(result.entries) != 0 {
		t.Fatal("accepted candidate wired to another immutable parent revision")
	}
}

func TestStagedReferencePlanRejects(t *testing.T) {
	for name, mutate := range map[string]func(*stagedRolloutInventory){
		"claim": func(i *stagedRolloutInventory) { i.rollouts[1].claimedRolloutID = stagedRootA },
		"old-id": func(i *stagedRolloutInventory) {
			i.rollouts[1].before = bytes.Replace(i.rollouts[1].before, []byte(stagedChild), []byte(stagedGrandchild), 1)
		},
		"new-id": func(i *stagedRolloutInventory) {
			i.rollouts[1].after = bytes.Replace(i.rollouts[1].after, []byte(stagedChild), []byte(stagedGrandchild), 1)
		},
		"duplicate":      func(i *stagedRolloutInventory) { i.rollouts = append(i.rollouts, i.rollouts[2]) },
		"missing-parent": func(i *stagedRolloutInventory) { i.rollouts = append(i.rollouts[:2], i.rollouts[3:]...) },
		"attached": func(i *stagedRolloutInventory) {
			for _, p := range []*[]byte{&i.rollouts[1].before, &i.rollouts[1].after} {
				*p = bytes.Replace(*p, []byte("\"history_mode\":"), []byte("\"attached_thread\":true,\"history_mode\":"), 1)
			}
		},
		"unknown-schema": func(i *stagedRolloutInventory) { i.schemaPin = "unknown" },
		"incomplete":     func(i *stagedRolloutInventory) { i.complete = false },
		"parse-problem":  func(i *stagedRolloutInventory) { i.problems = []string{"unreadable archive"} },
		"bad-json":       func(i *stagedRolloutInventory) { i.rollouts[0].before = []byte("{") },
		"root-header": func(i *stagedRolloutInventory) {
			i.rollouts[2].after = bytes.Replace(i.rollouts[2].after, []byte("\"history_mode\":"), []byte("\"extra\":1,\"history_mode\":"), 1)
		},
		"session-change": func(i *stagedRolloutInventory) {
			i.rollouts[1].after = bytes.Replace(i.rollouts[1].after, []byte("\"session_id\":\""+stagedSession), []byte("\"session_id\":\""+stagedRootA), 1)
		},
		"compressed-name": func(i *stagedRolloutInventory) { i.rollouts[1].plainBasename += ".zst" },
		"bad-time": func(i *stagedRolloutInventory) {
			i.rollouts[1].plainBasename = strings.Replace(i.rollouts[1].plainBasename, "10-26T", "02-30T", 1)
		},
		"path": func(i *stagedRolloutInventory) { i.rollouts[1].plainBasename = "dir/" + i.rollouts[1].plainBasename },
	} {
		t.Run(name, func(t *testing.T) {
			i := stagedFixture(t)
			mutate(&i)
			if result, err := validateStagedRolloutReferences(i); err == nil || len(result.entries) != 0 || len(result.changedRootPayloads) != 0 {
				t.Fatal("accepted invalid or returned partial plan")
			}
		})
	}
	if _, err := validateStagedRolloutReferences(stagedRolloutInventory{}); err == nil {
		t.Fatal("zero inventory accepted")
	}
	if result, err := validateStagedRolloutReferences(stagedRolloutInventory{schemaPin: referenceSchemaPin, complete: true}); err != nil || len(result.entries) != 0 {
		t.Fatal("complete empty inventory unsupported")
	}
}
