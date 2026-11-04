package gateway

import (
	"encoding/json"
	"github.com/yetone/magpie/internal/localdictionary"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestLocalDictionaryHTTPBudgetAndRecovery(t *testing.T) {
	fresh(t)
	const word = "SYNTHETIC_PRIVATE_LITERAL"
	if _, e := localdictionary.Update("new", "replace", "text", word); e != nil {
		t.Fatal(e)
	}
	v := &echoing{}
	serveOn(t, "pa", "ka", []string{"m"}, v)
	s := New()
	for _, counts := range [][]int{{4097}, {2100, 2100}} {
		var messages []map[string]string
		for _, n := range counts {
			messages = append(messages, map[string]string{"role": "user", "content": strings.Repeat(word+" ", n)})
		}
		ask, _ := json.Marshal(map[string]any{"model": "pa/m", "messages": messages})
		code, body := postAs(t, s, "", string(ask))
		if code != http.StatusServiceUnavailable || v.count() != 0 || !strings.Contains(body, "nothing was sent") || strings.Contains(body, word) {
			t.Fatal("budget did not block before upstream", code, v.count())
		}
	}
	ask := `{"model":"pa/m","messages":[{"role":"user","content":"` + word + `"}]}`
	code, body := postAs(t, s, "", ask)
	if code != 200 || v.count() != 1 || strings.Contains(v.last(), word) || !strings.Contains(body, word) {
		t.Fatal("normal request did not recover", code, v.count())
	}
	if e := os.WriteFile(localdictionary.Path(), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	code, body = postAs(t, s, "", ask)
	if code != http.StatusServiceUnavailable || v.count() != 1 || strings.Contains(body, word) {
		t.Fatal("corrupt library reached upstream", code, v.count())
	}
}
func TestLocalDictionaryVideoAndStateShareBudget(t *testing.T) {
	fresh(t)
	const word = "SYNTHETIC_PRIVATE_LITERAL"
	if _, e := localdictionary.Update("new", "replace", "text", word); e != nil {
		t.Fatal(e)
	}
	extra := map[string]any{"negative_prompt": strings.Repeat(word+" ", 3000), "other_prompt": strings.Repeat(word+" ", 3000)}
	var hits maskHits
	if e := maskStrings(extra, &hits); e == nil {
		t.Fatal("video fields reset budget")
	}
	if _, _, _, e := maskedState(strings.Repeat(word+" ", 2100), strings.Repeat(word+" ", 2100), ""); e == nil {
		t.Fatal("state fields reset budget")
	}
}
