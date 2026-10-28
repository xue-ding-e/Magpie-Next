package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

const referenceSchemaPin = "970111f6cb62cc02fe36a82ea7d71b37a2889c97"

// referenceRewriteInput carries caller-established provenance, not proof of it.
// In particular parentPlan must belong to parentRolloutID's decoded old/new
// bytes; neither the schema pin nor cli_version establishes file identity.
type referenceRewriteInput struct {
	schemaPin                   string
	parentRolloutID             string
	oldOffset, exclusiveOrdinal uint64
	parentPlan                  boundaryPlan
}

type referenceNumberSpan struct{ start, end int }

// validateReferenceRewrite permits only the history_base byte-offset number to
// change. Everything else, including opaque fields, escapes, whitespace and
// line terminators, remains byte-identical. It validates no full native schema,
// discovers no files and authorizes no publication. Attached relations and root
// histories are outside this planning leaf's supported subset.
func validateReferenceRewrite(before, after []byte, input referenceRewriteInput) (boundaryPlan, error) {
	if input.schemaPin != referenceSchemaPin || input.parentRolloutID == "" {
		return nil, fmt.Errorf("unknown reference provenance")
	}
	replacement, ok := input.parentPlan.rebaseCutoff(input.oldOffset, input.exclusiveOrdinal)
	if !ok {
		return nil, fmt.Errorf("unmapped parent cutoff")
	}
	oldHeader, oldTail := referenceHeader(before)
	newHeader, newTail := referenceHeader(after)
	oldSpan, err := validateReferenceHeader(oldHeader, input.parentRolloutID, input.oldOffset, input.exclusiveOrdinal)
	if err != nil {
		return nil, err
	}
	newSpan, err := validateReferenceHeader(newHeader, input.parentRolloutID, replacement, input.exclusiveOrdinal)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(oldTail, newTail) || !bytes.Equal(oldHeader[:oldSpan.start], newHeader[:newSpan.start]) || !bytes.Equal(oldHeader[oldSpan.end:], newHeader[newSpan.end:]) {
		return nil, fmt.Errorf("reference rewrite changed other bytes")
	}
	return planRolloutBoundaries(before, after)
}

func referenceHeader(data []byte) ([]byte, []byte) {
	if n := bytes.IndexByte(data, '\n'); n >= 0 {
		return data[:n+1], data[n+1:]
	}
	return data, nil
}

func validateReferenceHeader(header []byte, parent string, offset, ordinal uint64) (referenceNumberSpan, error) {
	var span referenceNumberSpan
	// Conservative candidate resource bounds, not native format limits.
	if len(header) > 1<<20 {
		return span, fmt.Errorf("reference header exceeds supported size")
	}
	decoder := json.NewDecoder(bytes.NewReader(header))
	decoder.UseNumber()
	value, err := readReferenceJSON(decoder, nil, 0, &span)
	if err != nil {
		return span, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return span, fmt.Errorf("trailing header data")
	}
	root, ok := value.(map[string]any)
	if !ok || root["type"] != "session_meta" {
		return span, fmt.Errorf("not session metadata")
	}
	payload, ok := root["payload"].(map[string]any)
	if !ok || payload["history_mode"] != "paginated" {
		return span, fmt.Errorf("not paginated metadata")
	}
	if attached, exists := payload["attached_thread"]; exists && attached != false {
		return span, fmt.Errorf("attached or unknown relation")
	}
	base, ok := payload["history_base"].(map[string]any)
	if !ok || len(base) != 3 || base["thread_id"] != parent {
		return span, fmt.Errorf("unsupported history base")
	}
	if !referenceUintEquals(base["end_ordinal_exclusive"], ordinal) || !referenceUintEquals(base["end_byte_offset"], offset) || span.end == 0 {
		return span, fmt.Errorf("history base cutoff mismatch")
	}
	return span, nil
}

func referenceUintEquals(value any, want uint64) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	got, err := strconv.ParseUint(number.String(), 10, 64)
	return err == nil && got == want
}

// Decode with duplicate-key rejection at every depth. Only the target number's
// source span is used for conservation: decoded strings are never compared as
// a substitute for preserving opaque source bytes (including unusual escapes).
func readReferenceJSON(decoder *json.Decoder, path []string, depth int, span *referenceNumberSpan) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("reference header exceeds supported depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			var object = make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("invalid object key")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON key")
				}
				childPath := append(append([]string(nil), path...), key)
				value, err := readReferenceJSON(decoder, childPath, depth+1, span)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			var array []any
			for decoder.More() {
				value, err := readReferenceJSON(decoder, append(append([]string(nil), path...), "[]"), depth+1, span)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter")
		}
	}
	if len(path) == 3 && path[0] == "payload" && path[1] == "history_base" && path[2] == "end_byte_offset" {
		number, ok := token.(json.Number)
		if !ok {
			return nil, fmt.Errorf("non-numeric byte offset")
		}
		span.end = int(decoder.InputOffset())
		span.start = span.end - len(number.String())
	}
	return token, nil
}
