package legacycodex

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

// previewJSON retains spans, not reserialized unknown values. The node and
// depth budgets bound this short-lived tree even for tiny, deeply nested input.
type previewJSON struct {
	start, end int
	kind       byte
	text       string
	fields     map[string]*previewJSON
	elements   []*previewJSON
}

func (v *previewJSON) stringValue() string {
	if v == nil || v.kind != '"' {
		return ""
	}
	return v.text
}

func parsePreviewJSON(line []byte, limits previewLimits) (*previewJSON, string) {
	if !utf8.Valid(line) || !json.Valid(line) || !previewScalarEscapes(line) {
		return nil, "invalid_json"
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	values := 0
	root, code := walkPreviewJSON(d, line, 0, 1, &values, limits)
	if code != "" {
		return nil, code
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, "invalid_json"
	}
	return root, ""
}

func walkPreviewJSON(d *json.Decoder, line []byte, cursor, depth int, values *int, limits previewLimits) (*previewJSON, string) {
	*values++
	if depth > limits.depth || *values > limits.values {
		return nil, "resource_limit"
	}
	// cursor comes from the parent BEFORE More/Token can consume whitespace.
	// json.Valid already established that these separators are grammatical.
	for cursor < len(line) && bytes.IndexByte([]byte(" \t\r\n,:"), line[cursor]) >= 0 {
		cursor++
	}
	token, err := d.Token()
	if err != nil || cursor >= len(line) {
		return nil, "invalid_json"
	}
	var node previewJSON
	node.start = cursor
	switch value := token.(type) {
	case json.Delim:
		node.kind = byte(value)
		switch value {
		case '{':
			node.fields = make(map[string]*previewJSON)
			for d.More() {
				keyToken, err := d.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok {
					return nil, "invalid_json"
				}
				if _, exists := node.fields[key]; exists {
					return nil, "duplicate_key"
				}
				child, code := walkPreviewJSON(d, line, int(d.InputOffset()), depth+1, values, limits)
				if code != "" {
					return nil, code
				}
				node.fields[key] = child
			}
		case '[':
			cursor = int(d.InputOffset())
			for d.More() {
				child, code := walkPreviewJSON(d, line, cursor, depth+1, values, limits)
				if code != "" {
					return nil, code
				}
				node.elements = append(node.elements, child)
				cursor = int(d.InputOffset())
			}
		default:
			return nil, "invalid_json"
		}
		if _, err := d.Token(); err != nil {
			return nil, "invalid_json"
		}
	case string:
		node.kind, node.text = '"', value
	case json.Number:
		node.kind = '0'
	case nil:
		node.kind = 'n'
	case bool:
		node.kind = 'b'
	default:
		return nil, "invalid_json"
	}
	node.end = int(d.InputOffset())
	return &node, ""
}

// This lexical check runs only on valid JSON and UTF-8. encoding/json replaces
// unpaired surrogate escapes; a preview must reject that lossy interpretation,
// including in keys or unknown values. Ordinary escaped backslashes are skipped.
func previewScalarEscapes(line []byte) bool {
	inString := false
	for i := 0; i < len(line); i++ {
		if line[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || line[i] != '\\' {
			continue
		}
		i++
		if line[i] != 'u' {
			continue
		}
		value, _ := strconv.ParseUint(string(line[i+1:i+5]), 16, 16)
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(line) || line[i+1] != '\\' || line[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(line[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
