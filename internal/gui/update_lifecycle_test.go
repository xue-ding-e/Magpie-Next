package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/update"
)

// heldUpdateNotes keeps the real HTTP request pending until its owner ends
// or the test lets it answer. No wall-clock delay makes the request slow.
type heldUpdateNotes struct {
	entered  chan string
	canceled chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (h *heldUpdateNotes) answer() { h.once.Do(func() { close(h.release) }) }

func heldNotesUpdater(t *testing.T, kind string) (*updater, *heldUpdateNotes, context.CancelFunc) {
	t.Helper()
	sandboxHome(t)
	wasVersion, wasContext := Version, backendCtx
	Version = "0.1.10"
	ctx, cancel := context.WithCancel(wasContext)
	backendCtx = ctx
	t.Cleanup(func() { Version, backendCtx = wasVersion, wasContext })
	var h = &heldUpdateNotes{entered: make(chan string, 1), canceled: make(chan struct{}), release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kind == "relang" || r.URL.Path == "/notes" {
			h.entered <- r.URL.RawQuery
			select {
			case <-h.release:
			case <-r.Context().Done():
				close(h.canceled)
				return
			}
		}
		if r.URL.Path == "/notes" {
			json.NewEncoder(w).Encode(map[string]any{"releases": []update.Note{
				{Version: "0.1.11", Notes: "**Urgent release:** fixture fix"},
			}})
			return
		}
		json.NewEncoder(w).Encode(update.Release{Version: "0.1.11", Notes: "fixture notes in " + r.URL.Query().Get("lang")})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MAGPIE_UPDATE_FEED", srv.URL+"/feed")
	t.Setenv("MAGPIE_NOTES_FEED", srv.URL+"/notes")
	var u = &updater{lang: "zh"}
	// This is the same boundary a fixture must honor before restoring its
	// environment or Version: cancel its owner, then join its registered work.
	t.Cleanup(func() {
		cancel()
		h.answer()
		u.loops.Wait()
	})
	if kind == "urgent" {
		u.check()
	} else {
		u.state, u.notesIn = "available", "en"
		u.latest = &update.Release{Version: "0.1.11", Notes: "original English notes"}
		u.jsonIn("zh")
	}
	select {
	case query := <-h.entered:
		want := "after=0.1.10&lang=zh&upto=0.1.11"
		if kind == "relang" {
			want = "lang=zh"
		}
		if query != want {
			t.Fatalf("%s updater %p asked %q, want %q", kind, u, query, want)
		}
		t.Logf("held %s request belongs to updater %p: %s", kind, u, query)
	case <-time.After(5 * time.Second):
		t.Fatalf("%s updater never asked the loopback fixture", kind)
	}
	return u, h, cancel
}

// Both background paths must stop with the backend that owns them. The old
// unregistered goroutines used Background and kept these HTTP requests alive.
func TestUpdateNotesTasksEndWithBackend(t *testing.T) {
	for _, kind := range []string{"urgent", "relang"} {
		t.Run(kind, func(t *testing.T) {
			u, h, cancel := heldNotesUpdater(t, kind)
			cancel()
			u.loops.Wait()
			select {
			case <-h.canceled:
			case <-time.After(3 * time.Second):
				t.Fatalf("%s request outlived its updater's canceled backend and task join", kind)
			}
		})
	}
}

// Holding a notes request must not hold the state reader or the caller that
// starts it; while the owner lives, the eventual answer still takes effect.
func TestUpdateNotesTasksStayAsync(t *testing.T) {
	for _, kind := range []string{"urgent", "relang"} {
		t.Run(kind, func(t *testing.T) {
			u, h, _ := heldNotesUpdater(t, kind)
			read := make(chan updateJSON, 1)
			var readers sync.WaitGroup
			readers.Go(func() { read <- u.json() })
			t.Cleanup(readers.Wait)
			select {
			case j := <-read:
				if j.State != "available" || j.Latest != "0.1.11" {
					t.Fatalf("state while notes are held: %+v", j)
				}
			case <-time.After(5 * time.Second):
				// Release before Fatal's cleanups join the reader too.
				h.answer()
				t.Fatal("held notes request blocked the updater's state reader")
			}
			h.answer()
			u.loops.Wait()
			j := u.json()
			if kind == "urgent" && j.Quiet {
				t.Fatal("owned asynchronous urgent notes did not light the update pill")
			}
			if kind == "relang" && j.Notes != "fixture notes in zh" {
				t.Fatalf("owned asynchronous language refresh lost its answer: %+v", j)
			}
		})
	}
}

func TestUpdateUrgentNotesKeepOwnerVersion(t *testing.T) {
	u, h, _ := heldNotesUpdater(t, "urgent")
	// The old updater's HTTP request is already held. A later fixture's
	// Version must never become the base of that updater's urgency record.
	Version = "0.1.604"
	h.answer()
	u.loops.Wait()
	u.mu.Lock()
	b := u.behind
	u.mu.Unlock()
	if b.Base != "0.1.10" || b.Urgent != "0.1.11" {
		t.Fatalf("old updater recorded urgency for a later fixture: %+v", b)
	}
}

// Reuse the actual earlier test's updater and fixture restoration, rather
// than only a new updater that happens to ask for the same version interval.
func TestUpdateAvailableFixtureEndsItsNotes(t *testing.T) {
	wasContext := backendCtx
	ctx, cancel := context.WithCancel(wasContext)
	backendCtx = ctx
	t.Cleanup(func() { backendCtx = wasContext })
	queries := make(chan string, 2)
	canceled := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.RawQuery
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-release:
			json.NewEncoder(w).Encode(map[string]any{"releases": []update.Note{}})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MAGPIE_NOTES_FEED", srv.URL)
	t.Cleanup(func() { cancel(); once.Do(func() { close(release) }) })
	t.Run("available-page-owner", func(t *testing.T) {
		defer cancel() // end the owner before its fixture cleanups run
		TestAvailableOpensTheNewestPage(t)
		updates.mu.Lock()
		lang := updates.notesIn
		updates.mu.Unlock()
		want := map[string]bool{
			"after=0.1.10&lang=" + lang + "&upto=0.1.11": true,
			"after=0.1.10&lang=" + lang + "&upto=0.1.12": true,
		}
		for range 2 {
			select {
			case query := <-queries:
				if !want[query] {
					t.Fatalf("updater %p from TestAvailableOpensTheNewestPage asked %q", updates, query)
				}
				delete(want, query)
				t.Logf("TestAvailableOpensTheNewestPage updater %p owns held request %s", updates, query)
			case <-time.After(5 * time.Second):
				t.Fatal("earlier updater did not ask for both releases' notes")
			}
		}
	})
	// The child restored its Version, HOME and feed. Both of its requests
	// must already be ending; otherwise a later notes fixture inherits them.
	for range 2 {
		select {
		case <-canceled:
		case <-time.After(3 * time.Second):
			t.Fatal("TestAvailableOpensTheNewestPage restored its fixture with an old updater's notes request still alive")
		}
	}
}
