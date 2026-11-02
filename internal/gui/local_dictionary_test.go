package gui

import (
	"github.com/yetone/magpie/internal/localdictionary"
	"github.com/yetone/magpie/internal/masking"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalDictionaryAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := Handler(nil, nil)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	w := call("POST", "/api/settings/local-dictionary", `{"revision":"new","mode":"replace","format":"text","content":"synthetic-api-password"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "synthetic-api-password") {
		t.Fatal("metadata leaks content")
	}
	w = call("GET", "/api/settings/local-dictionary", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-api-password") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	w = call("GET", "/api/settings/local-dictionary?reveal=1&limit=100", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "synthetic-api-password") {
		t.Fatal(w.Code, w.Body)
	}
	w = call("POST", "/api/settings/local-dictionary", `{"revision":"new","mode":"replace","format":"text","content":"replacement"}`)
	if w.Code != http.StatusConflict {
		t.Fatal(w.Code, w.Body)
	}
	out, n := (masking.Masker{}).Text("before synthetic-api-password after")
	if n != 1 || strings.Contains(out, "synthetic-api-password") {
		t.Fatal("not used by masker")
	}
	if s, e := localdictionary.Read(0, 0, false); e != nil || s.Count != 1 {
		t.Fatal(s, e)
	}
}
func TestLocalDictionaryAPIGuards(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := Handler(nil, nil)
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/api/settings/local-dictionary", strings.NewReader(`{}`))
		r.Header.Set("Origin", "null")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal("frame reached dictionary", w.Code)
		}
		r = httptest.NewRequest(method, "/api/settings/local-dictionary", strings.NewReader(`{}`))
		w = httptest.NewRecorder()
		webGuard("synthetic-cookie", "synthetic-key", 0, h).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Fatal("unsigned web request reached dictionary")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/settings/local-dictionary", strings.NewReader(`{"revision":"new","mode":"replace","format":"text","content":"never-save"} {}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatal("accepted trailing JSON", w.Code)
	}
	s, e := localdictionary.Read(0, 0, false)
	if e != nil || s.Count != 0 {
		t.Fatal("invalid request changed dictionary", s, e)
	}
}
func TestLocalDictionaryAPIRejectsInvalidTextAndNull(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := Handler(nil, nil)
	for _, body := range []string{"{\"revision\":\"new\",\"mode\":\"replace\",\"format\":\"text\",\"content\":\"\xff\"}", `{"revision":"new","mode":"replace","format":"text","content":null}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/settings/local-dictionary", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatal("invalid request accepted", w.Code)
		}
		s, e := localdictionary.Read(0, 0, false)
		if e != nil || s.Count != 0 {
			t.Fatal("invalid request changed library", e)
		}
	}
}
