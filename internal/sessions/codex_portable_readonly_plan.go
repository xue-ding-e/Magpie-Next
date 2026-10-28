package sessions

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// stagedRolloutBytes carries caller-staged, decoded bytes. plainBasename must
// be the actual native plain filename, not an invented metadata-derived name.
// Its provenance and any decompression are outside this in-memory validator.
type stagedRolloutBytes struct {
	plainBasename, claimedRolloutID string
	before, after                   []byte
}

type stagedRolloutInventory struct {
	schemaPin string
	complete  bool // A caller assertion, never proof of disk traversal completeness.
	problems  []string
	rollouts  []stagedRolloutBytes
}

// readOnlyReferencePlan is an inventory-bound report, never publication
// authorization. Root payload semantics, native projections, locks, backups,
// graph discovery, and crash recovery still require independent validation.
// Only value summaries escape; no caller byte slice or mutable boundary map
// is retained. Input buffers must not be modified concurrently with validation.
type readOnlyReferencePlan struct {
	entries             []readOnlyRolloutSummary
	changedRootPayloads []string
}

type readOnlyRolloutSummary struct {
	rolloutID, parentRolloutID string
	beforeHash, afterHash      [32]byte
	beforeBytes, afterBytes    uint64
}

type stagedRolloutMetadata struct {
	threadID, sessionID, parentID string
	parentOffset, parentOrdinal   uint64
}

func validateStagedRolloutReferences(inventory stagedRolloutInventory) (readOnlyReferencePlan, error) {
	var empty readOnlyReferencePlan
	if inventory.schemaPin != referenceSchemaPin || !inventory.complete || len(inventory.problems) != 0 {
		return empty, fmt.Errorf("unknown or incomplete staged inventory")
	}
	var graph = rolloutPlanInventory{complete: true}
	var byID = make(map[string]stagedRolloutBytes, len(inventory.rollouts))
	var metadata = make(map[string]stagedRolloutMetadata, len(inventory.rollouts))
	for _, rollout := range inventory.rollouts {
		threadID, rolloutID, err := stagedRolloutIdentity(rollout.plainBasename)
		if err != nil {
			return empty, err
		}
		if rolloutID != rollout.claimedRolloutID {
			return empty, fmt.Errorf("claimed rollout identity differs from filename")
		}
		// Reject before map insertion: identical duplicates are still ambiguous.
		if _, exists := byID[rolloutID]; exists {
			return empty, fmt.Errorf("duplicate staged rollout identity")
		}
		oldMeta, err := readStagedMetadata(rollout.before)
		if err != nil {
			return empty, err
		}
		newMeta, err := readStagedMetadata(rollout.after)
		if err != nil {
			return empty, err
		}
		if oldMeta.threadID != threadID || newMeta.threadID != threadID || oldMeta.sessionID != newMeta.sessionID || oldMeta.parentID != newMeta.parentID {
			return empty, fmt.Errorf("staged metadata identity changed or differs from filename")
		}
		graph.nodes = append(graph.nodes, rolloutPlanNode{rolloutID: rolloutID, parentRolloutID: oldMeta.parentID})
		byID[rolloutID], metadata[rolloutID] = rollout, oldMeta
	}
	order, err := orderRolloutPlan(graph)
	if err != nil {
		return empty, err
	}
	var result readOnlyReferencePlan
	var plans = make(map[string]boundaryPlan, len(order))
	for _, id := range order {
		rollout, meta := byID[id], metadata[id]
		var plan boundaryPlan
		if meta.parentID == "" {
			oldHeader, oldTail := referenceHeader(rollout.before)
			newHeader, newTail := referenceHeader(rollout.after)
			if !bytes.Equal(oldHeader, newHeader) {
				return empty, fmt.Errorf("root metadata changed")
			}
			plan, err = planRolloutBoundaries(rollout.before, rollout.after)
			if !bytes.Equal(oldTail, newTail) {
				result.changedRootPayloads = append(result.changedRootPayloads, id)
			}
		} else {
			parentPlan, exists := plans[meta.parentID]
			if !exists {
				return empty, fmt.Errorf("parent plan unavailable")
			}
			plan, err = validateReferenceRewrite(rollout.before, rollout.after, referenceRewriteInput{
				schemaPin: inventory.schemaPin, parentRolloutID: meta.parentID,
				oldOffset: meta.parentOffset, exclusiveOrdinal: meta.parentOrdinal, parentPlan: parentPlan,
			})
		}
		if err != nil {
			return empty, fmt.Errorf("staged rollout %s: %w", id, err)
		}
		plans[id] = plan
		result.entries = append(result.entries, readOnlyRolloutSummary{
			rolloutID: id, parentRolloutID: meta.parentID, beforeHash: sha256.Sum256(rollout.before), afterHash: sha256.Sum256(rollout.after),
			beforeBytes: uint64(len(rollout.before)), afterBytes: uint64(len(rollout.after)),
		})
	}
	return result, nil
}

// Native ordinary filenames use one ID for thread and rollout; revert names
// append an immutable rollout ID after the stable thread ID. This deliberately
// supports only canonical lowercase UUIDs, exact timestamps and plain basenames,
// not every spelling accepted by the native parser, paths or compressed names.
func stagedRolloutIdentity(name string) (string, string, error) {
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return "", "", fmt.Errorf("unsupported rollout basename")
	}
	core := strings.TrimSuffix(strings.TrimPrefix(name, "rollout-"), ".jsonl")
	if len(core) < 20 || core[19] != '-' {
		return "", "", fmt.Errorf("unsupported rollout basename")
	}
	const layout = "2006-01-02T15-04-05"
	stamp, err := time.Parse(layout, core[:19])
	if err != nil || stamp.Format(layout) != core[:19] {
		return "", "", fmt.Errorf("unsupported rollout timestamp")
	}
	ids := strings.Split(core[20:], "_")
	if len(ids) < 1 || len(ids) > 2 || !canonicalRolloutID(ids[0]) {
		return "", "", fmt.Errorf("unsupported rollout identity")
	}
	rolloutID := ids[0]
	if len(ids) == 2 {
		rolloutID = ids[1]
	}
	if !canonicalRolloutID(rolloutID) {
		return "", "", fmt.Errorf("unsupported revision identity")
	}
	return ids[0], rolloutID, nil
}

func canonicalRolloutID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func readStagedMetadata(data []byte) (stagedRolloutMetadata, error) {
	var meta stagedRolloutMetadata
	header, _ := referenceHeader(data)
	if len(header) > 1<<20 {
		return meta, fmt.Errorf("unsupported metadata size")
	}
	decoder := json.NewDecoder(bytes.NewReader(header))
	decoder.UseNumber()
	var span referenceNumberSpan
	value, err := readReferenceJSON(decoder, nil, 0, &span)
	if err != nil {
		return meta, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return meta, fmt.Errorf("trailing metadata data")
	}
	root, ok := value.(map[string]any)
	if !ok || root["type"] != "session_meta" {
		return meta, fmt.Errorf("missing session metadata")
	}
	payload, ok := root["payload"].(map[string]any)
	if !ok || payload["history_mode"] != "paginated" {
		return meta, fmt.Errorf("unsupported history mode")
	}
	if attached, exists := payload["attached_thread"]; exists && attached != false {
		return meta, fmt.Errorf("unsupported attached relation")
	}
	meta.threadID, _ = payload["id"].(string)
	meta.sessionID, _ = payload["session_id"].(string)
	// Native may default missing session_id; this narrower candidate requires it
	// explicitly. session_id identifies the root thread, not the rollout revision.
	if !canonicalRolloutID(meta.threadID) || !canonicalRolloutID(meta.sessionID) {
		return meta, fmt.Errorf("explicit canonical metadata identities required")
	}
	baseValue, exists := payload["history_base"]
	if !exists || baseValue == nil {
		return meta, nil
	}
	base, ok := baseValue.(map[string]any)
	if !ok || len(base) != 3 {
		return meta, fmt.Errorf("unsupported history base")
	}
	meta.parentID, _ = base["thread_id"].(string)
	if !canonicalRolloutID(meta.parentID) {
		return meta, fmt.Errorf("unsupported parent rollout identity")
	}
	readUint := func(value any) (uint64, error) {
		number, ok := value.(json.Number)
		if !ok {
			return 0, fmt.Errorf("non-numeric cutoff")
		}
		return strconv.ParseUint(number.String(), 10, 64)
	}
	meta.parentOffset, err = readUint(base["end_byte_offset"])
	if err != nil {
		return meta, err
	}
	meta.parentOrdinal, err = readUint(base["end_ordinal_exclusive"])
	if err != nil {
		return meta, err
	}
	return meta, nil
}
