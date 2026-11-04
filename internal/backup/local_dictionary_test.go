package backup

import (
	"encoding/json"
	"github.com/yetone/magpie/internal/localdictionary"
	"strings"
	"testing"
)

func TestLocalDictionaryExcludedFromBackup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const word = "synthetic-password-not-for-backup"
	if _, e := localdictionary.Update("new", "replace", "text", word); e != nil {
		t.Fatal(e)
	}
	for _, keys := range []bool{false, true} {
		b, e := Collect(keys, "test")
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(b)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), word) {
			t.Fatal("local dictionary escaped into backup")
		}
	}
}
