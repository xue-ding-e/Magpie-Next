package legacycodex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
)

// RolloutPreview contains only measurements of an in-memory proposal, never
// history content or a writable rollout. Version identifies this report shape,
// not the Codex binary or schema. A successful preview is not a migration plan:
// it has not discovered descendants, checked projections, or acquired ownership.
type RolloutPreview struct {
	Version        uint32
	SourceSHA256   [32]byte
	ProposedSHA256 [32]byte
	SourceBytes    uint64
	ProposedBytes  uint64
	ConvertedItems uint64
	Boundaries     []RolloutBoundary
}

// RolloutBoundary measures one physical JSONL record, including its original
// line ending. Offsets count decoded UTF-8 bytes, not runes or compressed bytes.
type RolloutBoundary struct {
	Ordinal                    uint64
	SourceStart, SourceEnd     uint64
	ProposedStart, ProposedEnd uint64
}

// ProposedCutoff matches BOTH a source byte boundary and its exclusive ordinal.
// It supports a later read-only index consistency check; it does not establish
// that an index, fork graph or source snapshot is complete or safe to publish.
func (p RolloutPreview) ProposedCutoff(sourceOffset, endOrdinalExclusive uint64) (uint64, bool) {
	if endOrdinalExclusive == 0 || endOrdinalExclusive > uint64(len(p.Boundaries)) {
		return 0, false
	}
	b := p.Boundaries[endOrdinalExclusive-1]
	if b.Ordinal != endOrdinalExclusive-1 || b.SourceEnd != sourceOffset {
		return 0, false
	}
	return b.ProposedEnd, true
}

// PreviewError exposes a fixed category and a one-based physical record number
// (zero for an input-wide limit). It never includes input, IDs or decoded text.
// Unsupported categories are this preview's limits, not native corruption claims.
type PreviewError struct {
	Code   string
	Record int
}

func (e *PreviewError) Error() string {
	return fmt.Sprintf("rollout preview: %s at record %d", e.Code, e.Record)
}

type previewLimits struct {
	bytes, line, records, depth, values int
}

// These are local resource policies, not Codex or capsule protocol limits.
func defaultPreviewLimits() previewLimits {
	return previewLimits{bytes: 32 << 20, line: 16 << 20, records: 100000, depth: 64, values: 65536}
}

// PreviewAIToolboxRollout examines caller-supplied decoded JSONL without I/O or
// changing its input. Only recognized compaction values in response_item.payload
// and compacted.payload.replacement_history are proposed for expansion, using
// ExpandAIToolboxCompaction. Every byte outside those exact values is retained.
// Native reasoning, opaque ciphertext and call IDs are not interpreted or fixed.
// This is not a full native schema validator: other payloads and metadata values
// are preserved, not certified as acceptable to Codex.
//
// The initial supported subset is newline-terminated paginated root histories
// with one leading session_meta, ordinals 0..N-1 and no declared lineage. Legacy,
// inherited/copied metadata and other ordinal sequences may be legitimate native
// histories; they receive an explicit unsupported error, not guessed repairs.
// This subset follows the pinned Codex contract, not a version-string heuristic:
// https://github.com/openai/codex/blob/5425d3e625daa58eea3ad1de27c087eb3b2ffbd3/codex-rs/rollout/src/ordinal.rs
// https://github.com/openai/codex/blob/5425d3e625daa58eea3ad1de27c087eb3b2ffbd3/codex-rs/protocol/src/protocol.rs
// Checkpoint metadata alignment follows history/src/rollout_payload.rs at that
// same pin; its bytes and item order remain unchanged, not rebuilt projections.
//
// Limits are 32 MiB input AND proposed bytes, 16 MiB per physical record,
// 100,000 records, JSON depth 64, and 65,536 values per record. Duplicate object
// keys, invalid Unicode, incomplete tails and known unreadable capsules fail
// closed with a zero report. No partial result, plaintext or rollout is returned.
// A caller must not mutate decoded concurrently. Do not connect this function to
// portLine/portRollout: writer/reader coordination, backups, projections, source
// selection, fork closure and crash-safe publication remain separate obligations.
func PreviewAIToolboxRollout(decoded []byte) (RolloutPreview, error) {
	return previewRollout(decoded, defaultPreviewLimits())
}

func previewRollout(decoded []byte, limits previewLimits) (RolloutPreview, error) {
	var plan RolloutPreview
	fail := func(code string, record int) (RolloutPreview, error) {
		return RolloutPreview{}, &PreviewError{Code: code, Record: record}
	}
	if len(decoded) > limits.bytes {
		return fail("resource_limit", 0)
	}
	if len(decoded) == 0 {
		return fail("unsupported_empty", 0)
	}
	if decoded[len(decoded)-1] != '\n' {
		return fail("incomplete_tail", 0)
	}
	proposed := sha256.New()
	for start, record := 0, 1; start < len(decoded); record++ {
		if record > limits.records {
			return fail("resource_limit", record)
		}
		end := start + bytes.IndexByte(decoded[start:], '\n') + 1
		line := decoded[start:end]
		if len(line) > limits.line {
			return fail("resource_limit", record)
		}
		root, code := parsePreviewJSON(line, limits)
		if code != "" {
			return fail(code, record)
		}
		if root.kind != '{' {
			return fail("unsupported_record", record)
		}
		ordinal, ok := root.fields["ordinal"].unsigned(line)
		if !ok || ordinal != uint64(record-1) {
			return fail("unsupported_ordinal_sequence", record)
		}
		kind := root.fields["type"].stringValue()
		payload := root.fields["payload"]
		if kind == "" || payload == nil {
			return fail("unsupported_record", record)
		}
		if record == 1 {
			if kind != "session_meta" || payload.kind != '{' || payload.fields["history_mode"].stringValue() != "paginated" {
				return fail("unsupported_history", record)
			}
			for _, key := range []string{"history_base", "subagent_history_start_ordinal", "forked_from_id", "forked_from_ordinal_exclusive", "parent_thread_id"} {
				if value := payload.fields[key]; value != nil && value.kind != 'n' {
					return fail("unsupported_lineage", record)
				}
			}
			if source := payload.fields["source"]; source != nil && source.kind == '{' && source.fields["subagent"] != nil {
				return fail("unsupported_lineage", record)
			}
			if payload.fields["thread_source"].stringValue() == "subagent" {
				return fail("unsupported_lineage", record)
			}
		} else if kind == "session_meta" {
			return fail("unsupported_copied_metadata", record)
		}
		edits, code := previewEdits(line, kind, payload)
		if code != "" {
			return fail(code, record)
		}
		size := len(line)
		for _, edit := range edits {
			size += len(edit.replacement) - (edit.end - edit.start)
		}
		if size > limits.line || uint64(size) > uint64(limits.bytes)-plan.ProposedBytes {
			return fail("resource_limit", record)
		}
		cursor := 0
		for _, edit := range edits {
			proposed.Write(line[cursor:edit.start])
			proposed.Write(edit.replacement)
			cursor = edit.end
		}
		proposed.Write(line[cursor:])
		plan.Boundaries = append(plan.Boundaries, RolloutBoundary{
			Ordinal: ordinal, SourceStart: uint64(start), SourceEnd: uint64(end),
			ProposedStart: plan.ProposedBytes, ProposedEnd: plan.ProposedBytes + uint64(size),
		})
		plan.ProposedBytes += uint64(size)
		plan.ConvertedItems += uint64(len(edits))
		start = end
	}
	plan.Version = 1
	plan.SourceSHA256 = sha256.Sum256(decoded)
	copy(plan.ProposedSHA256[:], proposed.Sum(nil))
	plan.SourceBytes = uint64(len(decoded))
	return plan, nil
}

type previewEdit struct {
	start, end  int
	replacement []byte
}

func previewEdits(line []byte, kind string, payload *previewJSON) ([]previewEdit, string) {
	var items []*previewJSON
	switch kind {
	case "response_item":
		if payload.kind != '{' {
			return nil, "unsupported_record"
		}
		items = append(items, payload)
	case "compacted":
		if payload.kind != '{' {
			return nil, "unsupported_record"
		}
		list := payload.fields["replacement_history"]
		meta := payload.fields["replacement_history_metadata"]
		if list == nil || list.kind == 'n' {
			if meta != nil && meta.kind != 'n' {
				return nil, "unsupported_metadata_alignment"
			}
			return nil, ""
		}
		if list.kind != '[' {
			return nil, "unsupported_record"
		}
		if meta != nil && meta.kind != 'n' && (meta.kind != '[' || len(meta.elements) != len(list.elements)) {
			return nil, "unsupported_metadata_alignment"
		}
		if meta != nil {
			for _, value := range meta.elements {
				if value.kind != '{' {
					return nil, "unsupported_metadata_alignment"
				}
			}
		}
		items = list.elements
	default:
		return nil, ""
	}
	var edits []previewEdit
	for _, item := range items {
		if item.kind != '{' {
			return nil, "unsupported_item"
		}
		if item.fields["type"].stringValue() != "compaction" {
			continue
		}
		var value map[string]any
		d := json.NewDecoder(bytes.NewReader(line[item.start:item.end]))
		d.UseNumber()
		if err := d.Decode(&value); err != nil {
			return nil, "invalid_json"
		}
		replacement, changed, err := ExpandAIToolboxCompaction(value)
		if err != nil {
			return nil, "unreadable_known_compaction"
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(replacement)
		if err != nil {
			return nil, "unsupported_item"
		}
		edits = append(edits, previewEdit{start: item.start, end: item.end, replacement: encoded})
	}
	return edits, ""
}

func (v *previewJSON) unsigned(line []byte) (uint64, bool) {
	if v == nil || v.kind != '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(string(line[v.start:v.end]), 10, 64)
	return n, err == nil
}
