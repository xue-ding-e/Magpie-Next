package gateway

import (
	"bytes"
	"encoding/json"
)

// responsesWireBody leaves desktop bookkeeping with the client. The backend
// rejects internal_chat_message_metadata_passthrough on replayed items with
// only {"detail":"Bad Request"}. All model-visible content and seals stay.
func responsesWireBody(body []byte) []byte {
	const key = "internal_chat_message_metadata_passthrough"
	if !bytes.Contains(body, []byte(`"`+key+`"`)) {
		return body
	}
	var q map[string]json.RawMessage
	var input []json.RawMessage
	if json.Unmarshal(body, &q) != nil || json.Unmarshal(q["input"], &input) != nil {
		return body
	}
	changed := false
	for i, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if _, ok := item[key]; !ok {
			continue
		}
		delete(item, key)
		input[i], _ = marshalPlain(item)
		changed = true
	}
	if !changed {
		return body
	}
	q["input"], _ = marshalPlain(input)
	out, err := marshalPlain(q)
	if err != nil {
		return body
	}
	return out
}
