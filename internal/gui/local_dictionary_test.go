package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/localdictionary"
	"github.com/yetone/magpie/internal/masking"
)

const dictionaryCookie = "synthetic-dictionary-cookie"
const dictionaryRunKey = "synthetic-dictionary-run-key"

func dictionaryWeb() http.Handler {
	return webGuard(dictionaryCookie, dictionaryRunKey, 0, nil, Handler(nil, nil))
}

func dictionaryRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: dictionaryCookie, Value: dictionaryRunKey})
	return r
}

func TestLocalDictionaryExceptionsThroughAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := dictionaryWeb()
	post := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, dictionaryRequest("POST", "/api/settings/local-dictionary", body))
		if w.Code != http.StatusOK {
			t.Fatalf("dictionary mutation failed: HTTP %d", w.Code)
		}
	}
	post(`{"revision":"new","mode":"replace","format":"text","content":"synthetic-keep\nsynthetic-hide"}`)
	s, err := localdictionary.Read(0, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	post(`{"target":"allow","revision":"` + s.Revision + `","mode":"replace","format":"text","content":"synthetic-keep"}`)
	out, n := (masking.Masker{}).Text("synthetic-keep synthetic-hide")
	if n != 1 || !strings.Contains(out, "synthetic-keep") || strings.Contains(out, "synthetic-hide") {
		t.Fatal("saved exception did not override the masking list")
	}
	for _, reveal := range []bool{false, true} {
		path := "/api/settings/local-dictionary?target=allow"
		if reveal {
			path += "&reveal=1"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, dictionaryRequest("GET", path, ""))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "synthetic-keep") != reveal {
			t.Fatal("exception pagination disclosed content without reveal")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, dictionaryRequest("GET", "/api/settings/local-dictionary?target=invalid", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatal("invalid target silently selected another list")
	}
}

func TestLocalDictionaryAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := dictionaryWeb()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, dictionaryRequest(method, path, body))
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
		webGuard("synthetic-cookie", "synthetic-key", 0, nil, h).ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Fatal("unsigned web request reached dictionary")
		}
	}
	w := httptest.NewRecorder()
	dictionaryWeb().ServeHTTP(w, dictionaryRequest("POST", "/api/settings/local-dictionary", `{"revision":"new","mode":"replace","format":"text","content":"never-save"} {}`))
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
	h := dictionaryWeb()
	for _, body := range []string{"{\"revision\":\"new\",\"mode\":\"replace\",\"format\":\"text\",\"content\":\"\xff\"}", `{"revision":"new","mode":"replace","format":"text","content":null}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, dictionaryRequest("POST", "/api/settings/local-dictionary", body))
		if w.Code != http.StatusBadRequest {
			t.Fatal("invalid request accepted", w.Code)
		}
		s, e := localdictionary.Read(0, 0, false)
		if e != nil || s.Count != 0 {
			t.Fatal("invalid request changed library", e)
		}
	}
}

func TestLocalDictionaryAPITrustedTransportAndOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	const word = "synthetic-protected-dictionary-entry"
	s, err := localdictionary.Update("new", "replace", "text", word)
	if err != nil {
		t.Fatal(err)
	}
	development, production := Handler(nil, nil), dictionaryWeb()
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/settings/local-dictionary"},
		{"GET", "/api/settings/local-dictionary?reveal=1"},
		{"POST", "/api/settings/local-dictionary"},
	} {
		for _, tc := range []struct {
			name, origin, site  string
			development, cookie bool
			status              int
		}{
			{name: "development-no-origin", development: true, status: http.StatusForbidden},
			{name: "development-same-origin", development: true, origin: "http://dictionary.invalid", status: http.StatusForbidden},
			{name: "development-cookie", development: true, cookie: true, status: http.StatusForbidden},
			{name: "anonymous-web", status: http.StatusUnauthorized},
			{name: "foreign-origin", cookie: true, origin: "http://foreign.invalid", status: http.StatusForbidden},
			{name: "other-port", cookie: true, origin: "http://dictionary.invalid:9999", status: http.StatusForbidden},
			{name: "opaque-origin", cookie: true, origin: "null", status: http.StatusForbidden},
			{name: "cross-site", cookie: true, site: "cross-site", status: http.StatusForbidden},
		} {
			t.Run(route.method+route.path+"/"+tc.name, func(t *testing.T) {
				body := `{"revision":"` + s.Revision + `","mode":"replace","format":"text","content":"must-not-save"}`
				r := dictionaryRequest(route.method, "http://dictionary.invalid"+route.path, body)
				if !tc.cookie {
					r.Header.Del("Cookie")
				}
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", tc.site)
				h := production
				if tc.development {
					h = development
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.status || strings.Contains(w.Body.String(), word) {
					t.Fatalf("untrusted request reached dictionary: status=%d want=%d", w.Code, tc.status)
				}
			})
		}
	}
	after, err := localdictionary.Read(0, 100, true)
	if err != nil || after.Revision != s.Revision || len(after.Words) != 1 || after.Words[0] != word {
		t.Fatal("denied request changed dictionary", err)
	}
}

func TestLocalDictionaryAPIJSONAndNativeTransport(t *testing.T) {
	for _, transport := range []string{"web", "native"} {
		t.Run(transport, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			h := dictionaryWeb()
			if transport == "native" {
				h = ownWindow(Handler(nil, nil))
			}
			const body = `{"revision":"new","mode":"replace","format":"text","content":"synthetic-trusted-entry"}`
			for _, media := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=synthetic"} {
				r := dictionaryRequest("POST", "/api/settings/local-dictionary", body)
				r.Header.Set("Content-Type", media)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("non-JSON mutation accepted: media=%q status=%d", media, w.Code)
				}
			}
			r := dictionaryRequest("POST", "/api/settings/local-dictionary", body)
			r.Header.Set("Content-Type", "application/json; charset=utf-8")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("trusted JSON request rejected", w.Code)
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, dictionaryRequest("GET", "/api/settings/local-dictionary?reveal=1", ""))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "synthetic-trusted-entry") {
				t.Fatal("trusted explicit reveal failed", w.Code)
			}
		})
	}
}
