package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/provider"
)

// codexModelLists belongs to ListenAndServe. Its workers keep only raw
// backend replies in memory; the request serving a reply applies the user's
// current picks and remembers the original versions on its own goroutine.
type codexModelLists struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	closed  bool
	entries map[[32]byte]*codexModelListEntry
	// Resolve the local sign-in and proxy inside the worker. Keeping this
	// boundary per coordinator also lets tests hold a keychain-like read.
	request func(context.Context, *http.Request) (*http.Request, context.CancelFunc)
}

type codexModelListEntry struct {
	reply   codexModelListReply
	at      time.Time
	pending bool
}

type codexModelListReply struct {
	body        []byte
	etag        string
	contentType string
	status      int
}

func newCodexModelLists(ctx context.Context) *codexModelLists {
	ctx, cancel := context.WithCancel(ctx)
	return &codexModelLists{ctx: ctx, cancel: cancel, entries: make(map[[32]byte]*codexModelListEntry), request: codexModelListRequest}
}

func (c *codexModelLists) stop() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}

// makeRoom is called with mu held. Never evict a worker's entry.
func (c *codexModelLists) makeRoom() bool {
	if len(c.entries) < 32 {
		return true
	}
	var oldest [32]byte
	var at time.Time
	found := false
	for key, e := range c.entries {
		if !e.pending && (!found || e.at.Before(at)) {
			oldest, at, found = key, e.at, true
		}
	}
	if found {
		delete(c.entries, oldest)
	}
	return found
}

// A changed sign-in, backend, client version or capabilities gets its own
// list. Credentials are held only as a digest, never as map keys in clear.
func codexModelListKey(r *http.Request) [32]byte {
	b, _ := json.Marshal([]string{provider.CodexBase, r.URL.RawQuery,
		r.Header.Get("Authorization"), r.Header.Get("Chatgpt-Account-Id"),
		r.Header.Get("Cookie"), r.Header.Get("User-Agent"), r.Header.Get("Version"), r.Header.Get("Originator"),
		r.Header.Get("Openai-Beta"), r.Header.Get("X-Oai-Attestation"), r.Header.Get("X-Codex-Beta-Features")})
	return sha256.Sum256(b)
}

func (s *Server) fetchCodexModelList(req *http.Request) codexModelListReply {
	res, err := s.client.Do(req)
	if err != nil {
		return codexModelListReply{}
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return codexModelListReply{}
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return codexModelListReply{body: b, status: res.StatusCode, contentType: res.Header.Get("Content-Type")}
	}
	var list struct {
		Models []any `json:"models"`
	}
	if res.StatusCode >= 300 || json.Unmarshal(b, &list) != nil || list.Models == nil {
		return codexModelListReply{}
	}
	return codexModelListReply{body: b, status: res.StatusCode, etag: res.Header.Get("ETag")}
}

func codexModelListRequest(ctx context.Context, r *http.Request) (*http.Request, context.CancelFunc) {
	u := provider.CodexBase + "/models"
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	ctx, cancel := context.WithTimeout(ctx, codexModelsWait)
	ctx = provider.ViaSignedIn(ctx, "codex")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		cancel()
		return nil, func() {}
	}
	copyHeaders(req.Header, r.Header)
	req.Header.Del("Accept-Encoding")
	return req, cancel
}

func (s *Server) codexModelList(r *http.Request) codexModelListReply {
	c := s.codexLists
	if c == nil {
		req, cancel := codexModelListRequest(r.Context(), r)
		defer cancel()
		if req == nil {
			return codexModelListReply{}
		}
		return s.fetchCodexModelList(req)
	}
	key := codexModelListKey(r)
	c.mu.Lock()
	e := c.entries[key]
	closed := c.closed
	if e != nil && (e.pending || time.Since(e.at) < time.Minute) {
		reply := e.reply
		c.mu.Unlock()
		return reply
	}
	c.mu.Unlock()
	// The on-disk list belongs to this machine's Codex, not to another
	// client reaching the gateway over the network. Other callers warm a
	// cache only with a successful reply for their own credentials.
	disk := e == nil && local(r) && fromCodex(r.Header) && len(codexcat.CacheEntries()) > 0
	if closed {
		return codexModelListReply{}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return codexModelListReply{}
	}
	e = c.entries[key]
	if e == nil && !disk {
		c.mu.Unlock()
		req, cancel := codexModelListRequest(r.Context(), r)
		defer cancel()
		if req == nil {
			return codexModelListReply{}
		}
		req.Header.Del("If-None-Match")
		req.Header.Del("If-Modified-Since")
		reply := s.fetchCodexModelList(req)
		if reply.status != 0 {
			c.mu.Lock()
			if !c.closed && c.makeRoom() {
				c.entries[key] = &codexModelListEntry{reply: reply, at: time.Now()}
			}
			c.mu.Unlock()
		}
		return reply
	}
	if e == nil {
		// Bound cached identities; no in-flight entry is evicted.
		if !c.makeRoom() {
			c.mu.Unlock()
			return codexModelListReply{}
		}
		e = &codexModelListEntry{}
		c.entries[key] = e
	}
	reply := e.reply
	if e.pending || time.Since(e.at) < time.Minute {
		c.mu.Unlock()
		return reply
	}
	e.pending, e.at = true, time.Now()
	c.workers.Add(1)
	c.mu.Unlock()
	// Clone copies the caller's headers and URL before its handler returns.
	// Resolving the local sign-in's proxy can read a keychain, so even the
	// request's construction belongs to the worker rather than the picker.
	refresh := r.Clone(c.ctx)
	go func() {
		defer c.workers.Done()
		req, cancel := c.request(c.ctx, refresh)
		defer cancel()
		var next codexModelListReply
		if req != nil {
			// A client tag includes magpie's catalog. Revalidate only with
			// the backend tag whose unmodified body this cache holds.
			req.Header.Del("If-None-Match")
			req.Header.Del("If-Modified-Since")
			if reply.status != 0 && reply.etag != "" {
				req.Header.Set("If-None-Match", reply.etag)
			}
			next = s.fetchCodexModelList(req)
		}
		c.mu.Lock()
		// A 304, like a failed read, keeps the body already validated.
		if next.status != 0 && !c.closed {
			e.reply = next
		}
		e.pending = false
		c.mu.Unlock()
	}()
	return reply
}
