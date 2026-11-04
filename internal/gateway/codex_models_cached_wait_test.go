package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// An existing list gives Codex a useful answer even while ChatGPT cannot
// answer. Hold the backend independently of the short cached-list budget;
// the old three-second wait must not consume this whole second.
func TestCodexCachedModelListDoesNotWaitForBackend(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	release := make(chan struct{})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(`{"etag":"\"old\"","models":[{"slug":"gpt-6.1-sol","priority":1}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", CodexPath+"/models", nil)
	req.Header.Set("Authorization", "Bearer chatgpt-token")
	req.Header.Set("User-Agent", "codex_cli_rs/0.162.1")
	req.RemoteAddr = "127.0.0.1:1234"
	s := New()
	s.codexLists = newCodexModelLists(context.Background())
	defer s.codexLists.stop()
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	defer func() { close(release); <-done }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a cached model list still waited for the unavailable ChatGPT backend")
	}
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"gpt-6.1-sol"`) || !strings.Contains(rec.Body.String(), `"fake/m1"`) {
		t.Fatalf("cached list: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCodexModelListCacheRefreshAndIdentity(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	var calls atomic.Int32
	var version atomic.Int32
	version.Store(1)
	release := make(chan struct{})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		v := version.Load()
		if v == 2 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if v == 3 {
			w.WriteHeader(503)
			return
		}
		if v == 4 {
			w.WriteHeader(401)
			return
		}
		if v == 5 {
			if got := r.Header.Get("If-None-Match"); got != `"a-2"` {
				t.Errorf("backend revalidation tag = %q, want its original tag", got)
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"`+r.Header.Get("Authorization")+`-`+string(rune('0'+v))+`"`)
		w.Write([]byte(`{"models":[{"slug":"` + r.Header.Get("Authorization") + `-` + r.URL.Query().Get("client_version") + `-` + string(rune('0'+v)) + `"}]}`))
	})
	s := New()
	s.codexLists = newCodexModelLists(context.Background())
	defer s.codexLists.stop()
	defer close(release)
	h := s.Handler()
	ask := func(account, query string) (*httptest.ResponseRecorder, *http.Request) {
		t.Helper()
		r := httptest.NewRequest("GET", CodexPath+"/models?client_version="+query, nil)
		r.Header.Set("Authorization", account)
		r.Header.Set("Chatgpt-Account-Id", account)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w, r
	}
	a, req := ask("a", "one")
	b, _ := ask("b", "one")
	c, _ := ask("a", "two")
	if !strings.Contains(a.Body.String(), `"a-one-1"`) || !strings.Contains(b.Body.String(), `"b-one-1"`) || !strings.Contains(c.Body.String(), `"a-two-1"`) || calls.Load() != 3 {
		t.Fatal("model lists were not isolated by credentials and client query")
	}
	age := func() {
		s.codexLists.mu.Lock()
		s.codexLists.entries[codexModelListKey(req)].at = time.Time{}
		s.codexLists.mu.Unlock()
	}
	version.Store(2)
	age()
	for i := 0; i < 4; i++ {
		old, _ := ask("a", "one")
		if !strings.Contains(old.Body.String(), `"a-one-1"`) || old.Header().Get("ETag") != a.Header().Get("ETag") {
			t.Fatal("a pending refresh replaced the usable list or its ETag")
		}
	}
	release <- struct{}{}
	s.codexLists.workers.Wait()
	fresh, _ := ask("a", "one")
	if calls.Load() != 4 || !strings.Contains(fresh.Body.String(), `"a-one-2"`) || fresh.Header().Get("ETag") == a.Header().Get("ETag") {
		t.Fatalf("refreshed list: calls=%d body=%s", calls.Load(), fresh.Body.String())
	}
	version.Store(3)
	age()
	ask("a", "one")
	s.codexLists.workers.Wait()
	kept, _ := ask("a", "one")
	if !strings.Contains(kept.Body.String(), `"a-one-2"`) || kept.Header().Get("ETag") != fresh.Header().Get("ETag") {
		t.Fatal("a backend failure discarded the last successful list")
	}
	version.Store(5)
	age()
	ask("a", "one")
	s.codexLists.workers.Wait()
	validated, _ := ask("a", "one")
	if !strings.Contains(validated.Body.String(), `"a-one-2"`) || validated.Header().Get("ETag") != fresh.Header().Get("ETag") {
		t.Fatal("a backend 304 discarded the validated list or changed its ETag")
	}
	version.Store(4)
	age()
	ask("a", "one")
	s.codexLists.workers.Wait()
	denied, _ := ask("a", "one")
	if denied.Code != 401 {
		t.Fatalf("renewed sign-in required: status %d", denied.Code)
	}
}

func TestCodexModelListCacheClientHeaders(t *testing.T) {
	for _, header := range []string{"Version", "X-Oai-Attestation", "X-Codex-Beta-Features"} {
		t.Run(header, func(t *testing.T) {
			setup(t, provider.Chat, &fake{t: t})
			var calls atomic.Int32
			chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Write([]byte(`{"models":[{"slug":"client-` + r.Header.Get(header) + `"}]}`))
			})
			s := New()
			s.codexLists = newCodexModelLists(context.Background())
			defer s.codexLists.stop()
			h := s.Handler()
			ask := func(version string) string {
				t.Helper()
				r := httptest.NewRequest("GET", CodexPath+"/models?client_version=0.162.1", nil)
				r.Header.Set("Authorization", "same-account")
				r.Header.Set("Chatgpt-Account-Id", "same-account")
				r.Header.Set("User-Agent", "codex_cli_rs/0.162.1")
				r.Header.Set(header, version)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("model list status %d", w.Code)
				}
				return w.Body.String()
			}
			first := ask("0.162.1")
			second := ask("0.163.0")
			again := ask("0.162.1")
			if !strings.Contains(first, `"client-0.162.1"`) || !strings.Contains(second, `"client-0.163.0"`) || !strings.Contains(again, `"client-0.162.1"`) || calls.Load() != 2 {
				t.Fatalf("model cache mixed %s headers: requests=%d, changed client received its own model=%t", header, calls.Load(), strings.Contains(second, `"client-0.163.0"`))
			}
		})
	}
}

func TestCodexModelListRefreshStopsWithServer(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	began := make(chan struct{})
	release := make(chan struct{})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		close(began)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New()
	s.codexLists = newCodexModelLists(ctx)
	defer s.codexLists.stop()
	r := httptest.NewRequest("GET", CodexPath+"/models", nil)
	r.Header.Set("Authorization", "a")
	key := codexModelListKey(r)
	s.codexLists.entries[key] = &codexModelListEntry{reply: codexModelListReply{status: 200, body: []byte(`{"models":[{"slug":"old"}]}`)}}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	select {
	case <-began:
	case <-time.After(5 * time.Second):
		t.Fatal("the background model refresh did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() { s.codexLists.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopping the server did not cancel and join its model refresh")
	}
	s.codexLists.mu.Lock()
	defer s.codexLists.mu.Unlock()
	if s.codexLists.entries[key].pending {
		t.Fatal("server shutdown left a model refresh running")
	}
}

func TestCodexModelListDiskCacheIsLocal(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"slug":"remote-account-model"}]}`))
	})
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(`{"models":[{"slug":"local-account-model"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New()
	s.codexLists = newCodexModelLists(context.Background())
	defer s.codexLists.stop()
	r := httptest.NewRequest("GET", CodexPath+"/models", nil)
	r.Header.Set("Authorization", "remote")
	r.Header.Set("User-Agent", "codex_cli_rs/0.162.1")
	r.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	s.codexModels(w, r)
	if !strings.Contains(w.Body.String(), `"remote-account-model"`) || strings.Contains(w.Body.String(), `"local-account-model"`) {
		t.Fatalf("remote request used the local Codex cache: %s", w.Body.String())
	}
}

func TestCodexCachedListDoesNotWaitForLocalSignIn(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"slug":"new"}]}`))
	})
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(`{"models":[{"slug":"old"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New()
	s.codexLists = newCodexModelLists(context.Background())
	began, release := make(chan struct{}), make(chan struct{})
	s.codexLists.request = func(ctx context.Context, r *http.Request) (*http.Request, context.CancelFunc) {
		close(began)
		select {
		case <-release:
		case <-ctx.Done():
		}
		if r.Header.Get("Authorization") != "before" || r.URL.RawQuery != "client_version=one" {
			t.Error("background refresh retained the caller's mutable headers or URL")
		}
		return codexModelListRequest(ctx, r)
	}
	r := httptest.NewRequest("GET", CodexPath+"/models?client_version=one", nil)
	r.Header.Set("Authorization", "before")
	r.Header.Set("User-Agent", "codex_cli_rs/0.162.1")
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(w, r); close(done) }()
	defer func() { close(release); <-done; s.codexLists.stop() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the cached model list waited for local sign-in/proxy resolution")
	}
	select {
	case <-began:
	case <-time.After(5 * time.Second):
		t.Fatal("the background sign-in read did not start")
	}
	if !strings.Contains(w.Body.String(), `"old"`) {
		t.Fatalf("cached list: %s", w.Body.String())
	}
	r.Header.Set("Authorization", "after")
	r.URL.RawQuery = "client_version=two"
}
