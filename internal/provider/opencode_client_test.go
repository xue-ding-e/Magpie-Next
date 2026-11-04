package provider

import (
	"container/list"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// isolateOpenCodeIDs gives a serial test its own counter, conversation
// cache and issued registry, restoring them without copying the mutex.
func isolateOpenCodeIDs(t *testing.T) {
	t.Helper()
	openCodeIDs.Lock()
	last, counter := openCodeIDs.last, openCodeIDs.counter
	sessions, order := openCodeIDs.sessions, openCodeIDs.order
	issued, issuedOrder := openCodeIDs.issued, openCodeIDs.issuedOrder
	openCodeIDs.last, openCodeIDs.counter = 0, 0
	openCodeIDs.sessions, openCodeIDs.order = map[string]*list.Element{}, list.New()
	openCodeIDs.issued, openCodeIDs.issuedOrder = map[string]*list.Element{}, list.New()
	openCodeIDs.Unlock()
	t.Cleanup(func() {
		openCodeIDs.Lock()
		openCodeIDs.last, openCodeIDs.counter = last, counter
		openCodeIDs.sessions, openCodeIDs.order = sessions, order
		openCodeIDs.issued, openCodeIDs.issuedOrder = issued, issuedOrder
		openCodeIDs.Unlock()
	})
}

// openCodeDecode reads an id as OpenCode's identifier.ts lays it out: the
// time in milliseconds (kept to 36 bits) and the count of ids made in it.
func openCodeDecode(t *testing.T, id string, descending bool) (ms, counter uint64) {
	t.Helper()
	v, err := strconv.ParseUint(id[4:16], 16, 64)
	if err != nil {
		t.Fatalf("%q: %v", id, err)
	}
	if descending {
		v = ^v & 0xffffffffffff
	}
	return v / 0x1000, v % 0x1000
}

// Zen's free tier answers 403 FreeTierError ("can only be used from within
// OpenCode") to a session id OpenCode couldn't have made (c9cu on Discord:
// muse-spark-1.3-contributor-free from Pi worked one day and not the
// next). Checked against opencode.ai with the same request:
// ses_e9283da055b15JwOAra76bwUx4, a sha256 cut to the shape that magpie
// sent, got 403; ses_ecc5d8f28ffe9Cy14V8Em7lI2u, from a real OpenCode
// 1.19.0 run, got 200. The session made is OpenCode's: descending, the
// time it was made, a small count.
func TestOpenCodeSessionIsOpenCodes(t *testing.T) {
	before := uint64(time.Now().UnixMilli()) % (1 << 36)
	id := openCodeSession("conversation-a")
	after := uint64(time.Now().UnixMilli()) % (1 << 36)
	if !openCodeIDRe.MatchString(id) || id[:4] != "ses_" || !openCodeRealSession(id) {
		t.Fatalf("session %q", id)
	}
	if ms, n := openCodeDecode(t, id, true); ms < before || ms > after || n < 1 || n >= 16 {
		t.Fatalf("session %q: time %d (want %d..%d), count %d", id, ms, before, after, n)
	}
	// one conversation keeps one session, another gets its own
	if again, other := openCodeSession("conversation-a"), openCodeSession("conversation-b"); again != id || other == id {
		t.Fatalf("sessions %q %q %q", id, again, other)
	}
	if a, b := openCodeSession(""), openCodeSession(""); a == b || !openCodeRealSession(a) {
		t.Fatalf("no conversation: %q %q", a, b)
	}
	// OpenCode's own goes as it is; a hash cut to its shape doesn't
	const real, hashed = "ses_ecc5d8f28ffe9Cy14V8Em7lI2u", "ses_e9283da055b15JwOAra76bwUx4"
	if got := openCodeSession(real); got != real {
		t.Fatalf("OpenCode's own session became %q", got)
	}
	if openCodeRealSession(hashed) {
		t.Fatal("a hash passes for OpenCode's session")
	}
	if got := openCodeSession(hashed); got == hashed || !openCodeRealSession(got) {
		t.Fatalf("a hashed session went on as %q", got)
	}
	h := http.Header{"X-Session-Affinity": {"from-the-agent"}}
	OpenCodeClient(h, "conversation-a")
	if h.Get("x-opencode-session") != id || h.Get("x-opencode-session-id") != id || h.Get("User-Agent") != OpenCodeUserAgent {
		t.Fatalf("headers %v", h)
	}
	// Zen's free tier refuses a request carrying x-session-affinity, which
	// OpenCode itself doesn't send it
	if h.Get("x-session-affinity") != "" || h.Get("X-Session-Id") != "" {
		t.Fatalf("headers OpenCode doesn't send: %v", h)
	}
	if req := h.Get("x-opencode-request"); req[:4] != "msg_" || !openCodeIDRe.MatchString(req) {
		t.Fatalf("request %q", req)
	}
}

// Ids made in one millisecond count up, as OpenCode's do; a request's is
// ascending.
func TestOpenCodeIDsCountUp(t *testing.T) {
	now := time.UnixMilli(1_790_000_000_000)
	first, second := openCodeNewID("msg", false, now), openCodeNewID("msg", false, now)
	if ms, n := openCodeDecode(t, first, false); ms != 1_790_000_000_000%(1<<36) || n != 1 {
		t.Fatalf("%q: time %d count %d", first, ms, n)
	}
	if _, n := openCodeDecode(t, second, false); n != 2 || second <= first {
		t.Fatalf("%q after %q: count %d", second, first, n)
	}
}

// Only the last openCodeKeep conversations' sessions are kept.
func TestOpenCodeSessionsKeptBounded(t *testing.T) {
	first := openCodeSession("bounded-0")
	for i := 1; i <= openCodeKeep; i++ {
		openCodeSession("bounded-" + strconv.Itoa(i))
	}
	openCodeIDs.Lock()
	n := openCodeIDs.order.Len()
	issued := openCodeIDs.issuedOrder.Len()
	openCodeIDs.Unlock()
	if n > openCodeKeep {
		t.Fatalf("%d kept", n)
	}
	if issued > openCodeKeep {
		t.Fatalf("%d issued sessions kept", issued)
	}
	if openCodeSession("bounded-0") == first {
		t.Fatal("the oldest conversation's session was still kept")
	}
}

// The native counter continues past 15 when a process issues many sessions
// in one millisecond. Its own sessions must still be recognized and reused.
func TestOpenCodeIssuedSessionsWithHighCounters(t *testing.T) {
	isolateOpenCodeIDs(t)
	now := time.UnixMilli(1_790_000_000_000)
	for n := uint64(1); n <= 80; n++ {
		id := openCodeNewID("ses", true, now)
		ms, counter := openCodeDecode(t, id, true)
		if ms != uint64(now.UnixMilli())%(1<<36) || counter != n {
			t.Fatalf("native session %q: time %d, count %d; want count %d", id, ms, counter, n)
		}
		if !openCodeRealSession(id) {
			t.Fatalf("own native session %q rejected at count %d", id, counter)
		}
		if got := openCodeSession(id); got != id {
			t.Fatalf("own native session at count %d became %q, want %q", counter, got, id)
		}
	}
}

func TestOpenCodeIssuedSessionsLRUAndHotCache(t *testing.T) {
	isolateOpenCodeIDs(t)
	now := time.UnixMilli(1_790_000_000_000)
	var hot string
	for n := uint64(1); n <= 32; n++ {
		hot = openCodeNewID("ses", true, now)
	}
	openCodeIDs.Lock()
	openCodeIDs.sessions["hot"] = openCodeIDs.order.PushBack(openCodeKept{seed: "hot", id: hot})
	openCodeIDs.Unlock()
	for n := 0; n < openCodeKeep; n++ {
		openCodeNewID("ses", true, now)
	}
	openCodeIDs.Lock()
	_, kept := openCodeIDs.issued[hot]
	issued := openCodeIDs.issuedOrder.Len()
	openCodeIDs.Unlock()
	if kept || issued != openCodeKeep {
		t.Fatalf("hot session registry entry=%v, size=%d", kept, issued)
	}
	if got := openCodeSession("hot"); got != hot {
		t.Fatalf("cached hot session became %q, want %q", got, hot)
	}
	openCodeIDs.Lock()
	_, restored := openCodeIDs.issued[hot]
	openCodeIDs.Unlock()
	if !restored || !openCodeRealSession(hot) {
		t.Fatal("hot cached session was not re-registered")
	}
	// Only the seed-cache hit refreshes hot here: checking recognition on
	// each turn would touch the registry and hide a missing cache-hit touch.
	for n := 0; n < openCodeKeep+32; n++ {
		openCodeNewID("ses", true, now)
		if got := openCodeSession("hot"); got != hot {
			t.Fatalf("hot session on turn %d became %q", n, got)
		}
	}
	openCodeIDs.Lock()
	issued, entries := openCodeIDs.issuedOrder.Len(), len(openCodeIDs.issued)
	conversations := len(openCodeIDs.sessions)
	openCodeIDs.Unlock()
	if issued != openCodeKeep || entries != openCodeKeep || conversations != 1 || !openCodeRealSession(hot) {
		t.Fatalf("hot session lost or cache grew: issued=%d, entries=%d, conversations=%d", issued, entries, conversations)
	}
}

// Recognition refreshes an issued id's LRU position. Once an untouched
// high-counter id leaves the registry, the unknown-id heuristic applies.
func TestOpenCodeIssuedSessionsTouchAndEvict(t *testing.T) {
	isolateOpenCodeIDs(t)
	now := time.UnixMilli(1_790_000_000_000)
	for n := 0; n < 15; n++ {
		openCodeNewID("msg", false, now)
	}
	hot := openCodeNewID("ses", true, now)
	cold := openCodeNewID("ses", true, now)
	for n := 2; n < openCodeKeep; n++ {
		openCodeNewID("ses", true, now)
	}
	if !openCodeRealSession(hot) {
		t.Fatal("recently issued high-counter session was rejected")
	}
	openCodeNewID("ses", true, now)
	if openCodeRealSession(cold) {
		t.Fatal("least recently used high-counter session was not evicted")
	}
	if !openCodeRealSession(hot) || openCodeSession(hot) != hot {
		t.Fatal("recognized session was not kept by its LRU touch")
	}
	openCodeIDs.Lock()
	entries, ordered := len(openCodeIDs.issued), openCodeIDs.issuedOrder.Len()
	openCodeIDs.Unlock()
	if entries != openCodeKeep || ordered != openCodeKeep {
		t.Fatalf("issued registry sizes: map=%d list=%d", entries, ordered)
	}
}

func TestOpenCodeUnknownSessionsKeepHeuristic(t *testing.T) {
	isolateOpenCodeIDs(t)
	now := time.UnixMilli(1_790_000_000_000)
	openCodeNewID("ses", true, now)
	for _, id := range []string{
		"ses_e9283da055b15JwOAra76bwUx4", // the old hash-shaped fixture
		"ses_e5b1338b7f91oofnFb3v2E2pd6", // native count 110, not issued here
		openCodeNewID("ses", false, now),
		openCodeNewID("msg", true, now),
		"ses_short", "ses_ecc5d8f28ffe9Cy14V8Em7lI2_",
	} {
		if openCodeRealSession(id) {
			t.Errorf("unknown or non-session id accepted: %q", id)
		}
	}
	const real = "ses_ecc5d8f28ffe9Cy14V8Em7lI2u"
	if !openCodeRealSession(real) || openCodeSession(real) != real {
		t.Fatal("the existing small-counter native fixture was not preserved")
	}
	openCodeIDs.Lock()
	entries := len(openCodeIDs.issued)
	openCodeIDs.Unlock()
	if entries != 1 {
		t.Fatalf("non-descending sessions or other prefixes were registered: %d entries", entries)
	}
}

func TestOpenCodeSessionsConcurrent(t *testing.T) {
	isolateOpenCodeIDs(t)
	now := time.UnixMilli(1_790_000_000_000)
	const workers = 64
	sessions := make(chan string, workers)
	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := openCodeNewID("ses", true, now)
			if !openCodeRealSession(id) || openCodeSession(id) != id {
				t.Errorf("concurrently issued native session was not preserved: %q", id)
			}
			sessions <- openCodeSession("concurrent-conversation")
		}()
	}
	wg.Wait()
	close(sessions)
	var first string
	for id := range sessions {
		if first == "" {
			first = id
		}
		if id != first || !openCodeRealSession(id) {
			t.Fatalf("concurrent conversation has sessions %q and %q", first, id)
		}
	}
	openCodeIDs.Lock()
	conversations, ordered := len(openCodeIDs.sessions), openCodeIDs.order.Len()
	openCodeIDs.Unlock()
	if conversations != 1 || ordered != 1 {
		t.Fatalf("concurrent cache sizes: map=%d list=%d", conversations, ordered)
	}
}
