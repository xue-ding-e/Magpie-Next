package gateway

import "encoding/json"

// This file is magpie's own (xue-ding-e/Magpie-Next), not a port of an
// upstream file whole: it holds the seal handling of c0074e55a
// ("a conversation on a new account leaves out the old one's seals",
// upstream b59e4b747, #3887) over the helpers this tree has.

// withoutNamedSeal repairs only the item the vendor identified. Reasoning
// and compaction have no usable plaintext fallback, so they go whole; a
// message or call keeps its identity and contents, losing only its seal.
// A native checkpoint is never removed to repair another account's turn.
func withoutNamedSeal(body []byte, id string, keepCompaction bool) ([]byte, bool) {
	var q map[string]json.RawMessage
	if decodeJSONView(body, &q) != nil {
		return nil, false
	}
	items, isList := inputList(q["input"])
	if !isList {
		return nil, false
	}
	kept, changed := items[:0:0], false
	for _, it := range items {
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if decodeJSONView(it, &item) == nil && item.ID == id {
			if sealedKinds[item.Type] {
				if item.Type != "reasoning" && keepCompaction {
					return nil, false
				}
				changed = true
				continue
			}
			if b, ok := withoutItemSeal(it); ok {
				it, changed = b, true
			}
		}
		kept = append(kept, it)
	}
	if !changed {
		return nil, false
	}
	q["input"], _ = json.Marshal(kept)
	b, err := marshalJSON(q)
	return b, err == nil
}
