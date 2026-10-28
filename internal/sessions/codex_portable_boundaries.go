package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf8"
)

// boundaryPlan relates exact record boundaries in two decoded JSONL buffers.
// This is a planning leaf, not a migration or a native-schema validator. The
// caller must independently validate payload semantics, history-base identity,
// projections and the complete descendant graph. Nothing here publishes data
// or makes the existing paginated-rollout refusal safe to remove.
type boundaryPlan map[uint64]plannedBoundary

type plannedBoundary struct {
	replacement      uint64
	exclusiveOrdinal uint64
}

func planRolloutBoundaries(before, after []byte) (boundaryPlan, error) {
	oldRecords, err := rolloutBoundaries(before)
	if err != nil {
		return nil, fmt.Errorf("original boundaries: %w", err)
	}
	newRecords, err := rolloutBoundaries(after)
	if err != nil {
		return nil, fmt.Errorf("replacement boundaries: %w", err)
	}
	if len(oldRecords) != len(newRecords) {
		return nil, fmt.Errorf("record count changed")
	}
	var plan boundaryPlan = make(boundaryPlan, len(oldRecords)+1)
	plan[0] = plannedBoundary{exclusiveOrdinal: oldRecords[0].ordinal}
	for i, old := range oldRecords {
		if old.ordinal != newRecords[i].ordinal {
			return nil, fmt.Errorf("record %d ordinal changed", i)
		}
		plan[old.end] = plannedBoundary{replacement: newRecords[i].end, exclusiveOrdinal: old.ordinal + 1}
	}
	return plan, nil
}

// rebaseBoundary rejects an interior byte, even if a nearby delta is known.
func (p boundaryPlan) rebaseBoundary(offset uint64) (uint64, bool) {
	v, ok := p[offset]
	return v.replacement, ok
}

// rebaseCutoff validates both parts of a history-base cutoff. A zero-byte
// prefix contains no source metadata and is not a supported fork prefix;
// zero exclusive ordinal is also rejected by native validate_cutoff_bounds.
// This verifies the pair only, not rollout identity or graph completeness.
func (p boundaryPlan) rebaseCutoff(offset, exclusiveOrdinal uint64) (uint64, bool) {
	v, ok := p[offset]
	if !ok || offset == 0 || exclusiveOrdinal == 0 || v.exclusiveOrdinal != exclusiveOrdinal {
		return 0, false
	}
	return v.replacement, true
}

type rolloutBoundary struct {
	ordinal uint64
	end     uint64
}

// rolloutBoundaries supports only nonempty, contiguous ordinal sequences.
// Native projections also support skipped ranges; those require a separate
// plan and are deliberately rejected here. Bytes are decoded bytes, including
// LF or CRLF terminators, never compressed-file positions or character counts.
func rolloutBoundaries(data []byte) ([]rolloutBoundary, error) {
	if len(data) == 0 || !utf8.Valid(data) {
		return nil, fmt.Errorf("empty or invalid UTF-8 rollout")
	}
	var records []rolloutBoundary
	for start := 0; start < len(data); {
		end := len(data)
		if n := bytes.IndexByte(data[start:], '\n'); n >= 0 {
			end = start + n + 1
		}
		ordinal, err := rolloutLineOrdinal(data[start:end])
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", len(records), err)
		}
		if ordinal == math.MaxUint64 {
			return nil, fmt.Errorf("exclusive ordinal overflow")
		}
		if len(records) > 0 && ordinal != records[len(records)-1].ordinal+1 {
			return nil, fmt.Errorf("non-contiguous ordinal")
		}
		records = append(records, rolloutBoundary{ordinal: ordinal, end: uint64(end)})
		start = end
	}
	return records, nil
}

// Read the exact ordinal key without encoding/json's case-insensitive struct
// matching or silently accepting duplicate ordinal members. The remaining JSON
// is validated but opaque; a valid map does not prove its payload is preserved.
func rolloutLineOrdinal(line []byte) (uint64, error) {
	if !json.Valid(line) {
		return 0, fmt.Errorf("invalid JSON record")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return 0, fmt.Errorf("record must be an object")
	}
	var ordinal uint64
	found := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, err
		}
		if key != "ordinal" {
			continue
		}
		if found || bytes.Equal(value, []byte("null")) {
			return 0, fmt.Errorf("duplicate or null ordinal")
		}
		if err := json.Unmarshal(value, &ordinal); err != nil {
			return 0, fmt.Errorf("invalid ordinal: %w", err)
		}
		found = true
	}
	if !found {
		return 0, fmt.Errorf("missing ordinal")
	}
	return ordinal, nil
}
