package sessions

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/codexsummary"
	"github.com/yetone/magpie/internal/steady"
)

func TestPortCodexRolloutsRevisitsShortPaginatedSummary(t *testing.T) {
	// The preexisting scan passed this file after the full Codex
	// summary prefix made the converted record too long.
	summary := strings.Repeat("Prior task state. ", 24)
	lines := portRolloutLines(summary)
	lines[0] = strings.Replace(lines[0], `"model_provider":"openai"`, `"model_provider":"openai","history_mode":"paginated"`, 1)
	home, path := portHome(t, lines, false)
	before := lineEnds(t, path)
	b := []byte(fmt.Sprintf(`{"homes":{%q:{"since":%q,"lineage":true}}}`, filepath.Clean(home)+codexLeftKey, time.Now().Format(time.RFC3339Nano)))
	if err := os.MkdirAll(filepath.Dir(codexPortPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := steady.WriteFile(codexPortPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := PortCodexRolloutsLeft(context.Background(), home)
	if err != nil || !res.Done || !reflect.DeepEqual(res.Rewritten, []string{path}) {
		t.Fatalf("old scan did not revisit the short checkpoint: %+v, %v", res, err)
	}
	got := readRollout(t, path)
	for _, i := range []int{2, 3} {
		if strings.Contains(got[i], codexsummary.Mark) || !strings.Contains(got[i], summary) {
			t.Fatalf("record %d did not preserve the complete summary as text", i)
		}
	}
	if ends := lineEnds(t, path); !reflect.DeepEqual(ends, before) || got[5] != lines[5] {
		t.Fatal("portable rewrite moved record boundaries or changed a native seal")
	}
}

// Codex 0.163.0-alpha.11 (5425d3e625daa58eea3ad1de27c087eb3b2ffbd3)
// stores both ordinals and byte offsets in its SQLite projection and in fork
// history_base. Replacing a compaction in-place moves the next record even when
// every ordinal and the number of lines stay unchanged, unless each decoded
// line keeps its byte length. Upstream's padded rewrites now do that, so these
// safe conversions must remain available rather than all paginated files being
// deferred. The existing longer-line test covers a rewrite that cannot fit.
func TestPortCodexRolloutsKeepsPaginatedReferences(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprint(packed), func(t *testing.T) {
			summary := strings.Repeat("Synthetic context detail. ", 80)
			lines := []string{
				`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated","cli_version":"0.163.0-alpha.11"}}`,
				`{"ordinal":1,"type":"response_item","payload":{"type":"compaction","encrypted_content":"` + codexsummary.Encrypted(summary) + `"}}`,
				`{"ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}}`,
			}
			home, path := portHome(t, lines, packed)
			parentBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			endsBefore := lineEnds(t, path)
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
				if err != nil || !res.Done || len(res.Repaired) != 0 {
					t.Fatalf("safe paginated conversion: %+v, %v", res, err)
				}
				if pass == 0 && !reflect.DeepEqual(res.Rewritten, []string{path}) || pass == 1 && len(res.Rewritten) != 0 {
					t.Fatalf("conversion or repeat pass: %+v", res)
				}
				parentAfter, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				childAfter, err := os.ReadFile(child)
				if err != nil {
					t.Fatal(err)
				}
				statAfter, err := os.Stat(path)
				if err != nil || !bytes.Equal(childAfter, childBefore) || !statAfter.ModTime().Equal(statBefore.ModTime()) || statAfter.Mode() != statBefore.Mode() {
					t.Fatal("conversion changed a fork reference, file mode, or modification time")
				}
				if endsAfter := lineEnds(t, path); !reflect.DeepEqual(endsBefore, endsAfter) {
					t.Fatalf("decoded record boundaries moved: %v became %v", endsBefore, endsAfter)
				}
				got := readRollout(t, path)
				if strings.Contains(got[1], codexsummary.Mark) || !strings.Contains(got[1], summary) {
					t.Fatal("safe conversion did not preserve the summary")
				}
				if pass == 1 && !bytes.Equal(parentAfter, parentBefore) {
					t.Fatal("repeat pass changed the converted rollout")
				}
				parentBefore = parentAfter
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
	if err != nil || !res.Done || len(res.Rewritten) != 0 || len(res.Repaired) != 0 {
		t.Fatalf("unchanged paginated history: %+v, %v", res, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("unchanged paginated history was written")
	}
}
