package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/localdictionary"
	"github.com/yetone/magpie/internal/settings"
)

func TestLocalDictionaryExceptionThroughHTTP(t *testing.T) {
	fresh(t)
	const visible = "synthetic-visible@example.com"
	const hidden = "SYNTHETIC_NEIGHBOR_SECRET"
	state, err := localdictionary.Update("new", "replace", "text", visible+"\n"+hidden)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = localdictionary.UpdateList("allow", state.Revision, "replace", "text", visible); err != nil {
		t.Fatal(err)
	}
	st := settings.Load()
	st.RedactPersonal, st.RedactWords = true, []string{visible}
	if err = settings.Save(st); err != nil {
		t.Fatal(err)
	}
	vendor := &echoing{}
	serveOn(t, "pa", "ka", []string{"m"}, vendor)
	server := New()
	body := `{"model":"pa/m","messages":[{"role":"user","content":"` + visible + " " + hidden + `"}]}`
	code, answer := postAs(t, server, "", body)
	if code != http.StatusOK || vendor.count() != 1 {
		t.Fatal("protected request did not reach its synthetic vendor", code, vendor.count())
	}
	if !strings.Contains(vendor.last(), visible) || strings.Contains(vendor.last(), hidden) {
		t.Fatal("the exception or its neighboring protection was lost before the HTTP send")
	}
	if !json.Valid([]byte(answer)) || !strings.Contains(answer, hidden) {
		t.Fatal("the protected answer was not restored as valid JSON")
	}
}

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
	if e := maskStrings(context.Background(), extra, &hits); e == nil {
		t.Fatal("video fields reset budget")
	}
	if _, _, _, e := maskedState(context.Background(), strings.Repeat(word+" ", 2100), strings.Repeat(word+" ", 2100), ""); e == nil {
		t.Fatal("state fields reset budget")
	}
}
