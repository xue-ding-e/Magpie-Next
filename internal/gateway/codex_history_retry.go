package gateway

import (
	"encoding/json"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/provider"
)

// ChatGPT's Codex backend has answered a large replay with only
// {"detail":"Bad Request"} below the model's token limit. This budget
// bounds one smaller retry of that specific refusal. It is not a limit
// on requests the backend can accept, including image-heavy turns.
const codexResponsesBodyBudget = 800 << 10

func codexHistoryRetry(res *http.Response, body []byte) ([]byte, bool) {
	if res.StatusCode != http.StatusBadRequest || len(body) < codexBareCapLeast ||
		!codexByteCapRefused(res.StatusCode, peekError(res), len(body)) {
		return body, false
	}
	return codexSmallerHistory(body)
}

// Leave the newest user turn, reasoning seals, checkpoints and call IDs
// intact. Only shorten text in tool results from earlier turns, and retry
// only if this brings the whole body below the budget.
func codexSmallerHistory(body []byte) ([]byte, bool) {
	items := getIn(body, "input").Array()
	latest := latestUser(provider.Responses, items)
	if latest < 1 {
		return body, false
	}
	q, err := rawObject(body)
	if err != nil {
		return body, false
	}
	var input []json.RawMessage
	if json.Unmarshal(q["input"], &input) != nil || len(input) != len(items) {
		return body, false
	}
	// Start with the most context, then lower the per-result allowance if
	// the fixed parts of the request still leave too little room.
	for _, edge := range []int{2048, 512, 128} {
		out := fitCodexOutputs(q, input, items[:latest], edge)
		if len(out) != 0 && len(out) <= codexResponsesBodyBudget && len(out) < len(body) {
			return out, true
		}
	}
	return body, false
}

func fitCodexOutputs(q map[string]json.RawMessage, input []json.RawMessage, history []gjson.Result, edge int) []byte {
	fitted := slices.Clone(input)
	changed := false
	for i, item := range history {
		switch item.Get("type").Str {
		case "function_call_output", "custom_tool_call_output":
		default:
			continue
		}
		if item.Get("encrypted_content").Exists() {
			continue
		}
		replacement := shortenCodexOutput(item.Get("output"), edge)
		if replacement == nil {
			continue
		}
		fields, err := rawObject(input[i])
		if err != nil {
			return nil
		}
		fields["output"] = replacement
		fitted[i], err = marshalPlain(fields)
		if err != nil {
			return nil
		}
		changed = true
	}
	if !changed {
		return nil
	}
	var err error
	q["input"], err = marshalPlain(fitted)
	if err != nil {
		return nil
	}
	out, _ := marshalPlain(q)
	return out
}

func shortenCodexOutput(output gjson.Result, edge int) json.RawMessage {
	shorten := func(s string) (string, bool) {
		if len(s) <= 4*edge {
			return s, false
		}
		left, right := edge, len(s)-edge
		for !utf8.RuneStart(s[left]) {
			left--
		}
		for !utf8.RuneStart(s[right]) {
			right++
		}
		return s[:left] + "\n[... older tool output shortened to fit the Codex request ...]\n" + s[right:], true
	}
	if output.Type == gjson.String {
		if s, ok := shorten(output.Str); ok {
			raw, _ := marshalPlain(s)
			return raw
		}
		return nil
	}
	if !output.IsArray() {
		return nil
	}
	var parts []json.RawMessage
	if json.Unmarshal([]byte(output.Raw), &parts) != nil {
		return nil
	}
	changed := false
	for i, part := range output.Array() {
		switch part.Get("type").Str {
		case "input_text", "output_text", "text":
		default:
			continue
		}
		if s, ok := shorten(part.Get("text").Str); ok {
			fields, err := rawObject(parts[i])
			if err != nil {
				return nil
			}
			fields["text"], _ = marshalPlain(s)
			parts[i], err = marshalPlain(fields)
			if err != nil {
				return nil
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	raw, _ := marshalPlain(parts)
	return raw
}
