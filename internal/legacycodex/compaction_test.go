package legacycodex

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Fixed synthetic nonce and public protocol material, never credentials.
func synthetic(t *testing.T, summary string) string {
	t.Helper()
	key := sha256.Sum256([]byte("ai-toolbox-remote-compaction"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(map[string]any{"summary": summary, "model": "synthetic", "created_at": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("synthetic123")
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return "ai-toolbox-compact-v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...))
}

func jsonBytes(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestKnownCompactionKeepsAssistantSummary(t *testing.T) {
	for name, summary := range map[string]string{
		"plain":                          "Synthetic context only",
		"unicode_and_whitespace":         " \n合成摘要 😀\t ",
		"marker_is_literal_summary_text": "reasoning_0; call_keep; magpie1: are synthetic summary text",
	} {
		t.Run(name, func(t *testing.T) {
			original := map[string]any{"type": "compaction", "id": "cmp_synthetic", "encrypted_content": synthetic(t, summary)}
			before := jsonBytes(t, original)
			got, changed, err := ExpandAIToolboxCompaction(original)
			want := map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "Context summary from previous turns:\n" + summary}}}
			if err != nil || !changed || !reflect.DeepEqual(got, want) {
				t.Fatal("known summary was not retained in the original consumer's role and content shape")
			}
			if before != jsonBytes(t, original) {
				t.Fatal("input item changed")
			}
			again, changed, err := ExpandAIToolboxCompaction(got)
			if err != nil || changed || !reflect.DeepEqual(again, got) {
				t.Fatal("expanding a proposed replacement was not idempotent")
			}
			got["content"].([]map[string]any)[0]["text"] = "mutated output only"
			if before != jsonBytes(t, original) {
				t.Fatal("output aliases the original item")
			}
		})
	}
}

func TestUnreadableOrUnsupportedKnownItemIsPreserved(t *testing.T) {
	valid := synthetic(t, "Synthetic context only")
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(valid, "ai-toolbox-compact-v1:"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	badTag := "ai-toolbox-compact-v1:" + base64.RawURLEncoding.EncodeToString(sealed)
	for name, item := range map[string]map[string]any{
		"bad_envelope":           {"type": "compaction", "encrypted_content": "ai-toolbox-compact-v1:invalid"},
		"invalid_authentication": {"type": "compaction", "encrypted_content": badTag},
		"blank_summary":          {"type": "compaction", "encrypted_content": synthetic(t, " \n\t ")},
		"unknown_field":          {"type": "compaction", "encrypted_content": valid, "synthetic_unknown": "must not disappear"},
		"invalid_id_type":        {"type": "compaction", "id": 7, "encrypted_content": valid},
	} {
		t.Run(name, func(t *testing.T) {
			before := jsonBytes(t, item)
			got, changed, err := ExpandAIToolboxCompaction(item)
			if err == nil || changed || got == nil || jsonBytes(t, got) != before || jsonBytes(t, item) != before {
				t.Fatal("known unsupported item was not retained with an explicit error")
			}
			if strings.Contains(err.Error(), item["encrypted_content"].(string)) {
				t.Fatal("error exposes input content")
			}
		})
	}
}

func TestOtherItemsKeepCiphertextCallIDsAndSummary(t *testing.T) {
	valid := synthetic(t, "Synthetic context only")
	for name, item := range map[string]map[string]any{
		"opaque_reasoning":             {"type": "reasoning", "id": "rs_synthetic", "summary": []any{}, "encrypted_content": "SYNTHETIC_OPAQUE_NOT_REAL"},
		"legacy_reasoning_with_cipher": {"type": "reasoning", "id": "reasoning_0", "summary": []any{map[string]any{"type": "summary_text", "text": "Synthetic context only"}}, "encrypted_content": "SYNTHETIC_OPAQUE_NOT_REAL"},
		"reasoning_with_known_capsule": {"type": "reasoning", "id": "reasoning_0", "encrypted_content": valid},
		"legacy_reasoning_unresolved":  {"type": "reasoning", "id": "reasoning_0", "summary": []any{map[string]any{"type": "summary_text", "text": "Synthetic context only"}}},
		"native_compaction":            {"type": "compaction", "encrypted_content": "SYNTHETIC_OPAQUE_NOT_REAL"},
		"unknown_capsule":              {"type": "compaction", "encrypted_content": "unknown:synthetic"},
		"unproven_alias":               {"type": "compaction_summary", "encrypted_content": valid},
		"tool_call":                    {"type": "function_call", "call_id": "call_keep", "name": "synthetic", "arguments": "{}", "encrypted_content": valid},
		"tool_output":                  {"type": "function_call_output", "call_id": "call_keep", "output": valid},
		"non_string_envelope":          {"type": "compaction", "encrypted_content": map[string]any{"synthetic": true}},
	} {
		t.Run(name, func(t *testing.T) {
			before := jsonBytes(t, item)
			got, changed, err := ExpandAIToolboxCompaction(item)
			if err != nil || changed || jsonBytes(t, got) != before || jsonBytes(t, item) != before {
				t.Fatal("non-target item changed")
			}
		})
	}
}
