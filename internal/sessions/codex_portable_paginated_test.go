package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/codexsummary"
)

// Codex 0.163.0-alpha.11 (5425d3e625daa58eea3ad1de27c087eb3b2ffbd3)
// stores both ordinals and byte offsets in its SQLite projection and in fork
// history_base. Replacing a compaction in-place moves the next record even when
// every ordinal and the number of lines stay unchanged.
func TestPortCodexRolloutsDefersPaginatedHistory(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprint(packed), func(t *testing.T) {
			lines := []string{
				`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated","cli_version":"0.163.0-alpha.11"}}`,
				`{"ordinal":1,"type":"response_item","payload":{"type":"compaction","encrypted_content":"` + codexsummary.Encrypted("Synthetic context only") + `"}}`,
				`{"ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}}`,
			}
			home, path := portHome(t, lines, packed)
			parentBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			statBefore, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// A child forks through ordinal 1. Its offset is in uncompressed
			// JSONL bytes even when the parent is stored as .zst.
			end := len(lines[0]) + 1 + len(lines[1]) + 1
			child := filepath.Join(filepath.Dir(path), "rollout-2026-10-20T15-06-20-01a15820-ff81-7840-bb3f-e18771c53aa9.jsonl")
			childBefore := []byte(fmt.Sprintf(`{"ordinal":2,"type":"session_meta","payload":{"id":"01a15820-ff81-7840-bb3f-e18771c53aa9","history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":2,"end_byte_offset":%d}}}`+"\n", portThread, end))
			if err := os.WriteFile(child, childBefore, 0o600); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				res, err := PortCodexRollouts(context.Background(), home)
				if err != nil || !res.Done || len(res.Rewritten) != 0 {
					t.Fatalf("paginated history must be deferred, not published with stale byte references: %+v, %v", res, err)
				}
				// Decode the public result so this test also compiles on the
				// baseline before Blocked was added and fails on behavior.
				resultJSON, _ := json.Marshal(res)
				var reported struct{ Blocked []string }
				if err := json.Unmarshal(resultJSON, &reported); err != nil || len(reported.Blocked) != 1 || reported.Blocked[0] != path {
					t.Fatalf("deferred path was not reported exactly once: %s", resultJSON)
				}
				parentAfter, _ := os.ReadFile(path)
				childAfter, _ := os.ReadFile(child)
				statAfter, err := os.Stat(path)
				if err != nil || !bytes.Equal(parentAfter, parentBefore) || !bytes.Equal(childAfter, childBefore) || !statAfter.ModTime().Equal(statBefore.ModTime()) {
					t.Fatal("deferral changed a rollout, fork reference, or modification time")
				}
				b, err := os.ReadFile(codexPortPath())
				if err != nil {
					t.Fatal(err)
				}
				var state codexPortState
				if err := json.Unmarshal(b, &state); err != nil {
					t.Fatal(err)
				}
				entry := state.Homes[filepath.Clean(home)]
				if !contains(entry.Pending, path) {
					t.Fatal("deferred rollout was not retained for a later safe migration")
				}
				// Put the scan watermark beyond this old rollout; Pending,
				// rather than mtime slack, must keep it eligible on pass two.
				entry.Since = time.Now().Add(time.Hour)
				state.Homes[filepath.Clean(home)] = entry
				b, _ = json.Marshal(state)
				if err := os.WriteFile(codexPortPath(), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPortCodexRolloutsLeavesUnchangedPaginatedHistory(t *testing.T) {
	lines := []string{
		`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated"}}`,
		`{"ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"magpie1: is only a mention"}]}}`,
	}
	home, path := portHome(t, lines, false)
	before, _ := os.ReadFile(path)
	res, err := PortCodexRollouts(context.Background(), home)
	resultJSON, _ := json.Marshal(res)
	var reported struct{ Blocked []string }
	if json.Unmarshal(resultJSON, &reported) != nil || err != nil || !res.Done || len(res.Rewritten) != 0 || len(reported.Blocked) != 0 {
		t.Fatalf("unchanged paginated history incorrectly blocked: %+v, %v", res, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("unchanged paginated history was written")
	}
}
