package sessions

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Characterizes the current Magpie-only marker scope, not successful migration.
// This minimal session_meta exercises Magpie detection only; it is not a complete
// native Codex schema fixture (for example, session_id is intentionally absent).
// Opaque reasoning is a preservation sentinel, not valid provider ciphertext.
// The second invocation checks persisted scan status and retained bytes; an
// unchanged old file need not be read again after the first pass watermark.
// All content, capsule key material and nonce below are public synthetic fixtures.
func TestPortSyntheticLegacyMarkersScope(t *testing.T) {
	key := sha256.Sum256([]byte("ai-toolbox-remote-compaction"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("synthetic123")
	plain := []byte(`{"summary":"Synthetic retained summary","model":"synthetic","created_at":1}`)
	capsule := "ai-toolbox-compact-v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, plain, nil)...))
	for _, packed := range []bool{false, true} {
		name := "plain"
		if packed {
			name = "zstd"
		}
		t.Run(name, func(t *testing.T) {
			lines := []string{
				`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated"}}`,
				`{"ordinal":1,"type":"response_item","payload":{"type":"reasoning","id":"reasoning_0","summary":[{"type":"summary_text","text":"Synthetic reasoning"}],"encrypted_content":null}}`,
				`{"ordinal":2,"type":"response_item","payload":{"type":"compaction","encrypted_content":"` + capsule + `"}}`,
				`{"ordinal":3,"type":"response_item","payload":{"type":"function_call","name":"fixture","call_id":"call_synthetic","arguments":"{}"}}`,
				`{"ordinal":4,"type":"response_item","payload":{"type":"function_call_output","call_id":"call_synthetic","output":"synthetic"}}`,
				`{"ordinal":5,"type":"response_item","payload":{"type":"reasoning","id":"rs_synthetic","summary":[{"type":"summary_text","text":"Preserve native-shaped summary"}],"encrypted_content":"OPAQUE_SYNTHETIC_NOT_REAL"}}`,
			}
			home, path := portHome(t, lines, packed)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				res, err := PortCodexRollouts(context.Background(), home)
				if err != nil || !res.Done || len(res.Rewritten)+len(res.Held)+len(res.Blocked) != 0 {
					t.Fatalf("current scope changed: pass=%d result=%+v err=%v", pass, res, err)
				}
				stateBytes, err := os.ReadFile(codexPortPath())
				if err != nil {
					t.Fatal(err)
				}
				var state codexPortState
				if err := json.Unmarshal(stateBytes, &state); err != nil {
					t.Fatal(err)
				}
				saved, exists := state.Homes[filepath.Clean(home)]
				if !exists || saved.Since.IsZero() {
					t.Fatal("scan completion state not persisted")
				}
				if len(saved.Pending) != 0 {
					t.Fatal("current legacy-only scope unexpectedly retained pending")
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				now, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || !now.ModTime().Equal(info.ModTime()) || now.Mode() != info.Mode() {
					t.Fatal("legacy-only history changed")
				}
			}
		})
	}
}
