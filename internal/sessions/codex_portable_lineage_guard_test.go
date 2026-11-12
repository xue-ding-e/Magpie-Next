package sessions

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// These are synthetic repair inputs, not a claim of complete native-schema or
// fork-graph validation. Uncertain sources must leave a child untouched and
// eligible for repair after the source becomes readable and unambiguous.
func TestRepairCodexLineagesKeepsUncertainSources(t *testing.T) {
	for _, problem := range []string{
		"duplicate source", "unreadable scan", "invalid metadata", "incomplete tail",
		"fractional ordinal", "duplicate ordinal", "fractional cutoff", "fractional offset", "missing source",
	} {
		t.Run(problem, func(t *testing.T) {
			lines := []string{
				`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated"}}`,
				`{"ordinal":1,"type":"event_msg","payload":{"type":"task_started"}}`,
			}
			home, parent := portHome(t, lines, false)
			const childID = "01a15820-ff81-7840-bb3f-e18771c53aa9"
			child := filepath.Join(filepath.Dir(parent), strings.Replace(filepath.Base(parent), portThread, childID, 1))
			before := []byte(fmt.Sprintf(`{"ordinal":2,"type":"session_meta","payload":{"id":%q,"history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":2,"end_byte_offset":14362}}}`+"\n", childID, portThread))
			walker := filepath.WalkDir
			parentData := []byte(strings.Join(lines, "\n") + "\n")
			switch problem {
			case "duplicate source":
				dir := filepath.Join(home, "archived_sessions")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, filepath.Base(parent)), parentData, 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable scan":
				walker = func(root string, visit fs.WalkDirFunc) error {
					if root == filepath.Join(home, "archived_sessions") {
						return fs.ErrPermission
					}
					return filepath.WalkDir(root, visit)
				}
			case "invalid metadata":
				parentData = []byte("synthetic invalid metadata\n" + lines[1] + "\n")
			case "incomplete tail":
				parentData = bytes.TrimSuffix(parentData, []byte("\n"))
			case "fractional ordinal":
				parentData = bytes.Replace(parentData, []byte(`"ordinal":1`), []byte(`"ordinal":1.5`), 1)
			case "duplicate ordinal":
				parentData = append(parentData, []byte(lines[1]+"\n")...)
			case "fractional cutoff":
				before = bytes.Replace(before, []byte(`"end_ordinal_exclusive":2`), []byte(`"end_ordinal_exclusive":2.5`), 1)
			case "fractional offset":
				before = bytes.Replace(before, []byte(`"end_byte_offset":14362`), []byte(`"end_byte_offset":14362.5`), 1)
			case "missing source":
				before = bytes.Replace(before, []byte(portThread), []byte("01a15820-0000-7840-bb3f-e18771c53aa9"), 1)
			}
			if err := os.WriteFile(parent, parentData, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(child, before, 0o600); err != nil {
				t.Fatal(err)
			}
			fixed, whole := repairLineages(context.Background(), home, walker)
			if whole || len(fixed) != 0 {
				t.Fatalf("uncertain lineage was repaired or marked complete: %v, %v", fixed, whole)
			}
			after, err := os.ReadFile(child)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("uncertain repair changed the child")
			}
		})
	}
}

// Native history can skip ordinal ranges. Keep those repairs, including exact
// u64 ordinals beyond float64's integer range and beyond signed int64.
func TestRepairCodexLineagesKeepsExactSkippedOrdinals(t *testing.T) {
	for _, ordinal := range []uint64{4, 9007199254740993, 1<<63 + 4} {
		t.Run(fmt.Sprint(ordinal), func(t *testing.T) {
			lines := []string{
				`{"ordinal":0,"type":"session_meta","payload":{"id":"` + portThread + `","history_mode":"paginated"}}`,
				fmt.Sprintf(`{"ordinal":%d,"type":"event_msg","payload":{"type":"task_started"}}`, ordinal),
			}
			home, parent := portHome(t, lines, false)
			const childID = "01a15820-ff81-7840-bb3f-e18771c53aa9"
			child := filepath.Join(filepath.Dir(parent), strings.Replace(filepath.Base(parent), portThread, childID, 1))
			before := []byte(fmt.Sprintf(`{"ordinal":%d,"type":"session_meta","payload":{"id":%q,"history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":%d,"end_byte_offset":14362}}}`+"\n", ordinal+1, childID, portThread, ordinal+1))
			if err := os.WriteFile(child, before, 0o600); err != nil {
				t.Fatal(err)
			}
			fixed, whole := repairLineages(context.Background(), home, filepath.WalkDir)
			if !whole || !reflect.DeepEqual(fixed, []string{child}) {
				t.Fatalf("supported repair lost: %v, %v", fixed, whole)
			}
			after, err := os.ReadFile(child)
			if err != nil || len(after) != len(before) {
				t.Fatal("repair changed child byte positions")
			}
			want := int64(len(strings.Join(lines, "\n")) + 1)
			if got := gjson.GetBytes(after, "payload.history_base.end_byte_offset").Int(); got != want {
				t.Fatalf("cutoff = %d, want %d", got, want)
			}
		})
	}
}
