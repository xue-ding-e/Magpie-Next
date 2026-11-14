package legacycodex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HistoryBasePreview measures one supplied history_base reference, not a fork
// graph or a writable child header. SourceEnd and ProposedEnd are decoded UTF-8
// byte cutoffs with the same EndOrdinalExclusive; the byte totals cover the
// whole parent. No history content, IDs, paths or rewritten JSON are returned.
type HistoryBasePreview struct {
	Version                                     uint32
	SourceSHA256, ProposedSHA256                [32]byte
	ChildHeaderSHA256                           [32]byte
	SourceBytes, ProposedBytes, ConvertedItems  uint64
	EndOrdinalExclusive, SourceEnd, ProposedEnd uint64
}

// HistoryBasePreviewError contains only a fixed category, never supplied data.
// Unsupported categories describe this helper's subset, not native corruption.
type HistoryBasePreviewError struct {
	Code string
}

func (e *HistoryBasePreviewError) Error() string { return "history base preview: " + e.Code }

// PreviewAIToolboxHistoryBase checks one child header against a freshly computed
// parent preview. It does not trust a caller-supplied RolloutPreview. The caller
// supplies the immutable source rollout ID and its expected decoded-byte hash;
// SessionMeta.id is a logical thread ID and is NOT substituted for that ID.
// This checks the supplied ID/hash binding, not filesystem identity, ownership,
// snapshot freshness or whether every dependent rollout has been discovered.
// Equal parent/child logical IDs are allowed: native same-thread revisions can
// have distinct rollout IDs. No child rollout ID is supplied here, so this is
// not a cycle check. Neither input may be mutated concurrently.
//
// The parent retains PreviewAIToolboxRollout's root-only subset. The child is
// exactly one newline-terminated paginated session_meta, with a three-field
// history_base and header ordinal equal to its exclusive cutoff K. Optional
// forked_from_id/ordinal metadata is accepted as a pair matching the logical
// parent ID and K. Inherited-origin forks can be legitimate but are unsupported
// here, as are subagent/copied-prefix lineage and unknown metadata keys. The
// supported source variants are absent (native default), cli/vscode/exec/mcp;
// thread_source is absent/null or user. Other known metadata values are not
// schema-validated. These are conservative policies, not a native validator.
//
// The fixed contract is HistoryPosition and SessionMeta in protocol/src/protocol.rs
// at Codex 5425d3e625daa58eea3ad1de27c087eb3b2ffbd3. That writer initializes
// child metadata at K, then advances to K+1 (rollout/src/ordinal.rs:23-31 and
// rollout/src/recorder.rs:1988-2013). The native cutoff-bounds check alone does
// not establish exact ordinal/record-end alignment; this helper checks both.
//
// There is no I/O, DB projection, child rewrite, lock, backup or publication.
// A successful result is not permission to migrate any history. All failures
// return a zero report. Parent limits are unchanged; the child header shares
// the preview's per-record byte, JSON-depth and value limits.
func PreviewAIToolboxHistoryBase(sourceRolloutID string, expectedSourceSHA256 [32]byte, decodedParent, childHeader []byte) (HistoryBasePreview, error) {
	return previewHistoryBase(sourceRolloutID, expectedSourceSHA256, decodedParent, childHeader, defaultPreviewLimits())
}

func previewHistoryBase(sourceRolloutID string, expectedSourceSHA256 [32]byte, decodedParent, childHeader []byte, limits previewLimits) (HistoryBasePreview, error) {
	fail := func(code string) (HistoryBasePreview, error) {
		return HistoryBasePreview{}, &HistoryBasePreviewError{Code: code}
	}
	if len(decodedParent) > limits.bytes || len(childHeader) > limits.line {
		return fail("resource_limit")
	}
	sourceID, ok := previewUUID(sourceRolloutID)
	if !ok {
		return fail("unsupported_source_id")
	}
	if sha256.Sum256(decodedParent) != expectedSourceSHA256 {
		return fail("source_hash_mismatch")
	}
	parent, err := previewRollout(decodedParent, limits)
	if err != nil {
		if failure, ok := err.(*PreviewError); ok {
			return fail("source_" + failure.Code)
		}
		return fail("source_preview_failed")
	}
	if len(childHeader) == 0 || bytes.IndexByte(childHeader, '\n') != len(childHeader)-1 {
		return fail("unsupported_child_header")
	}
	child, code := parsePreviewJSON(childHeader, limits)
	if code != "" {
		return fail("child_" + code)
	}
	if child.kind != '{' || child.fields["type"].stringValue() != "session_meta" {
		return fail("unsupported_child_header")
	}
	if !previewKnownHeader(child) {
		return fail("unsupported_child_metadata")
	}
	payload := child.fields["payload"]
	if payload == nil || payload.kind != '{' || payload.fields["history_mode"].stringValue() != "paginated" {
		return fail("unsupported_child_history")
	}
	if !previewKnownSessionMeta(payload) {
		return fail("unsupported_child_metadata")
	}
	if _, ok := previewUUID(payload.fields["id"].stringValue()); !ok {
		return fail("unsupported_child_header")
	}
	for _, key := range []string{"subagent_history_start_ordinal", "parent_thread_id"} {
		if value := payload.fields[key]; value != nil && value.kind != 'n' {
			return fail("unsupported_child_lineage")
		}
	}
	if !previewOrdinarySource(payload) {
		return fail("unsupported_child_lineage")
	}
	base := payload.fields["history_base"]
	if base == nil || base.kind != '{' || len(base.fields) != 3 {
		return fail("unsupported_history_base")
	}
	referencedID, validID := previewUUID(base.fields["thread_id"].stringValue())
	cutoffOrdinal, validOrdinal := base.fields["end_ordinal_exclusive"].unsigned(childHeader)
	cutoffByte, validByte := base.fields["end_byte_offset"].unsigned(childHeader)
	if !validID || !validOrdinal || !validByte {
		return fail("unsupported_history_base")
	}
	if referencedID != sourceID {
		return fail("source_id_mismatch")
	}
	childOrdinal, ok := child.fields["ordinal"].unsigned(childHeader)
	if !ok || childOrdinal != cutoffOrdinal {
		return fail("child_ordinal_mismatch")
	}
	// The parent preview has already checked its first record's shape and JSON.
	parentLine := decodedParent[:bytes.IndexByte(decodedParent, '\n')+1]
	parentMeta, code := parsePreviewJSON(parentLine, limits)
	if code != "" {
		return fail("source_" + code)
	}
	parentPayload := parentMeta.fields["payload"]
	if !previewKnownHeader(parentMeta) || !previewKnownSessionMeta(parentPayload) {
		return fail("unsupported_source_metadata")
	}
	if !previewOrdinarySource(parentPayload) {
		return fail("unsupported_source_lineage")
	}
	parentThreadID, validID := previewUUID(parentPayload.fields["id"].stringValue())
	if !validID {
		return fail("unsupported_source_identity")
	}
	forkID, forkOrdinal := payload.fields["forked_from_id"], payload.fields["forked_from_ordinal_exclusive"]
	hasForkID := forkID != nil && forkID.kind != 'n'
	hasForkOrdinal := forkOrdinal != nil && forkOrdinal.kind != 'n'
	if hasForkID != hasForkOrdinal {
		return fail("unsupported_child_lineage")
	}
	if hasForkID {
		logicalParentID, ok := previewUUID(forkID.stringValue())
		if !ok || logicalParentID != parentThreadID {
			return fail("unsupported_fork_origin")
		}
		ordinal, ok := forkOrdinal.unsigned(childHeader)
		if !ok || ordinal != cutoffOrdinal {
			return fail("fork_ordinal_mismatch")
		}
	}
	proposedEnd, ok := parent.ProposedCutoff(cutoffByte, cutoffOrdinal)
	if !ok {
		return fail("cutoff_mismatch")
	}
	return HistoryBasePreview{
		Version: 1, SourceSHA256: parent.SourceSHA256, ProposedSHA256: parent.ProposedSHA256,
		ChildHeaderSHA256: sha256.Sum256(childHeader), SourceBytes: parent.SourceBytes,
		ProposedBytes: parent.ProposedBytes, ConvertedItems: parent.ConvertedItems,
		EndOrdinalExclusive: cutoffOrdinal, SourceEnd: cutoffByte, ProposedEnd: proposedEnd,
	}, nil
}

func previewKnownHeader(header *previewJSON) bool {
	for key := range header.fields {
		if key != "timestamp" && key != "ordinal" && key != "type" && key != "payload" {
			return false
		}
	}
	return true
}

func previewOrdinarySource(payload *previewJSON) bool {
	if source := payload.fields["source"]; source != nil {
		switch source.stringValue() {
		case "cli", "vscode", "exec", "mcp":
		default:
			return false
		}
	}
	source := payload.fields["thread_source"]
	return source == nil || source.kind == 'n' || source.stringValue() == "user"
}

// SessionMetaLine flattens SessionMeta and adds git at the pinned contract.
// Unknown keys fail closed here even though native serde normally ignores them.
// This must not silently turn a future lineage-bearing field into an empty one.
func previewKnownSessionMeta(payload *previewJSON) bool {
	for key := range payload.fields {
		switch key {
		case "creator_user_id", "creator_account_id", "session_id", "id",
			"forked_from_id", "forked_from_ordinal_exclusive", "parent_thread_id",
			"timestamp", "cwd", "runtime_workspace_roots", "originator", "cli_version",
			"source", "thread_source", "agent_nickname", "agent_role", "agent_type", "agent_path",
			"model_provider", "base_instructions", "dynamic_tools", "selected_capability_roots",
			"memory_mode", "history_mode", "history_base", "subagent_history_start_ordinal",
			"multi_agent_version", "context_window", "git":
		default:
			return false
		}
	}
	// agent_type is the pinned serde alias of agent_role, not a second field.
	// Distinct JSON keys must not conceal two values for that one native field.
	// protocol/src/protocol.rs:3247,3286,3373-3392 derives SessionMeta's reader
	// and delegates SessionMetaLine's flattened fields to it at the pin above.
	return payload.fields["agent_role"] == nil || payload.fields["agent_type"] == nil
}

// UUIDs are compared as values, not names or case-sensitive text. Other native
// UUID spellings are outside this helper's canonical-hyphenated input subset.
func previewUUID(value string) ([16]byte, bool) {
	var id [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return id, false
	}
	raw := strings.ReplaceAll(value, "-", "")
	if len(raw) != 32 {
		return id, false
	}
	_, err := hex.Decode(id[:], []byte(raw))
	return id, err == nil
}
