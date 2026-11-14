// Package legacycodex plans narrowly identified legacy content conversions.
// It has no history reader, writer, lock, index or publication operation.
package legacycodex

import (
	"errors"

	"github.com/yetone/magpie/internal/capsule"
)

// contextHeading is the AI Toolbox consumer's heading, not Codex's native
// SUMMARY_PREFIX. Keeping its assistant role does not make this a native
// local-compaction marker or prove that a complete thread can be resumed.
const contextHeading = "Context summary from previous turns:"

var errItemShape = errors.New("known legacy compaction has an unsupported item shape; preserve the original")

// ExpandAIToolboxCompaction returns a proposed replacement for a known AI
// Toolbox compaction item. The caller's map is never changed. An unsupported
// item or unreadable capsule returns the original map and changed=false;
// errors never mean that an item may be omitted.
//
// This follows the pinned AI Toolbox consumer's assistant/output_text shape,
// rather than converting a third party's summary to Magpie's user-message
// shape. Unlike that consumer's trim, it preserves the decoded summary text.
// The stricter decoder and unknown-field rejection are local loss-prevention
// policies, not claims that the original producer rejects those inputs.
//
// A replacement is an in-memory proposal only. It does not authorize writing
// a rollout, establish complete migration, or validate its ordinals, decoded
// byte indexes, projections, source windows, writer locks or fork descendants.
// Do not wire it into portLine/portRollout until those obligations are met.
func ExpandAIToolboxCompaction(item map[string]any) (replacement map[string]any, changed bool, err error) {
	if item["type"] != "compaction" {
		return item, false, nil
	}
	enc, ok := item["encrypted_content"].(string)
	if !ok {
		return item, false, nil
	}
	summary, ours, err := capsule.Open(enc)
	if err != nil || !ours {
		return item, false, err
	}
	// Replacing a recognized item must not silently discard fields whose
	// meaning the pinned producer/consumer contract does not establish.
	for key := range item {
		if key != "type" && key != "id" && key != "encrypted_content" {
			return item, false, errItemShape
		}
	}
	if id, exists := item["id"]; exists {
		if _, ok := id.(string); !ok {
			return item, false, errItemShape
		}
	}
	return map[string]any{
		"type": "message", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": contextHeading + "\n" + summary}},
	}, true, nil
}
