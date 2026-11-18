package gateway

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/routes"
)

func TestResetPriorityTiesAndSink(t *testing.T) {
	fresh(t)
	now := time.Now()
	soon := now.Add(time.Hour)
	keyWindows(t, map[string]provider.Allowance{
		"a": week(5, now.Add(5*24*time.Hour)), "b": week(20, soon),
		"c": week(20, soon), "d": week(20, now.Add(2*time.Hour)),
	})
	var p provider.Provider
	p.ID, p.Routing = "tie", provider.Rotate
	p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week", Order: "soonest"}
	var cs []candidate
	for _, key := range []string{"a", "b", "c", "d"} {
		q := p
		q.Key = key
		cs = append(cs, candidate{p: q, rest: "tie#" + key, model: "m"})
	}
	var got string
	for range 6 {
		got += route(p, cs, "m", provider.Chat)[0].p.Key
	}
	if got != "bcbcbc" {
		t.Fatalf("equal earliest resets did not rotate as their own pool: %s", got)
	}
	p.Routing, p.Sink = provider.Ordered, true
	p.ResetPriority.Order = ""
	sink(cs[1].restKey(), now)
	t.Cleanup(func() { unsink(cs[1].restKey()) })
	cs = cs[:2] // only b qualifies, despite its previous rate limit
	if got := route(p, cs, "m", provider.Chat)[0].p.Key; got != "b" {
		t.Fatalf("Sink displaced the only preferred account: %s", got)
	}
	// A strategy switched off retains its original listed order inside
	// the preferred tier, while its ResetPriority remains in force.
	routes.Forget()
	p.Routing = "not-serving/strategy"
	if got := route(p, cs, "m", provider.Chat)[0].p.Key; got != "b" {
		t.Fatalf("missing strategy disabled reset priority: %s", got)
	}
}

func TestResetPriorityRespectsAccountCaps(t *testing.T) {
	codexSignedIn(t, "spare@example.com")
	b := newResetBackend(t)
	now := time.Now()
	old := allowances
	t.Cleanup(func() { allowances = old })
	allowances = func(string) map[string]provider.Allowance {
		return map[string]provider.Allowance{
			"me@example.com":    week(75, now.Add(time.Hour)),
			"spare@example.com": week(20, now.Add(5*24*time.Hour)),
		}
	}
	p, err := provider.Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week"}
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetAccountCap("codex", "me@example.com", 70); err != nil {
		t.Fatal(err)
	}
	if code, body := resetPost(t, New()); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if tried, _ := b.seen(); tried != "acct-2" {
		t.Fatalf("reset priority bypassed the account's usage cap: %s", tried)
	}
}

func TestResetPriorityUsesRemainingCodexQuota(t *testing.T) {
	for _, tc := range []struct {
		name        string
		used, short float64
		priority    bool
		refused     bool
		want        string
	}{
		{name: "one percent left", used: 99, priority: true, want: "acct-1"},
		{name: "used up", used: 100, priority: true, want: "acct-2"},
		{name: "another window used up", used: 99, short: 100, priority: true, want: "acct-2"},
		{name: "vendor refuses remaining quota", used: 99, priority: true, refused: true, want: "acct-1,acct-2"},
		{name: "ordinary smart routing", used: 99, want: "acct-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexSignedIn(t, "spare@example.com")
			var b *resetBackend
			if tc.refused {
				b = newResetBackend(t, "acct-1")
			} else {
				b = newResetBackend(t)
			}
			now := time.Now()
			quota := week(tc.used, now.Add(6*time.Hour))
			if tc.short > 0 {
				quota = append(quota, provider.Limit{Used: tc.short, Resets: now.Add(time.Hour), Span: 5 * time.Hour})
			}
			old := allowances
			t.Cleanup(func() { allowances = old })
			allowances = func(string) map[string]provider.Allowance {
				return map[string]provider.Allowance{
					"me@example.com":    quota,
					"spare@example.com": week(19, now.Add(5*24*time.Hour)),
				}
			}
			if tc.priority {
				p, err := provider.Find("codex")
				if err != nil {
					t.Fatal(err)
				}
				p.ResetPriority = &provider.ResetPriority{Hours: 48, Window: "any", Order: "soonest"}
				if err := provider.Save(*p); err != nil {
					t.Fatal(err)
				}
			}
			if code, body := resetPost(t, New()); code != 200 {
				t.Fatalf("%d %s", code, body)
			}
			if tried, _ := b.seen(); tried != tc.want {
				t.Fatalf("tried %s, want %s", tried, tc.want)
			}
		})
	}
}

func TestResetPriorityBurstStaysInTier(t *testing.T) {
	fresh(t)
	now := time.Now()
	keyWindows(t, map[string]provider.Allowance{
		"outside": week(10, now.Add(5*24*time.Hour)),
		"soon":    week(99, now.Add(time.Hour)),
		"later":   week(10, now.Add(2*time.Hour)),
	})
	var up keyed
	serveOn(t, "priority", "outside", []string{"m"}, &up, "soon", "later")
	p, err := provider.Find("priority")
	if err != nil {
		t.Fatal(err)
	}
	p.Routing = provider.Rotate
	p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week", Order: "soonest"}
	s := New()
	first, firstPlan := s.plan(*p, "m", provider.Chat)
	first, _, releaseFirst := burstSpread(p.Routing, nil, false, true, first, firstPlan)
	defer releaseFirst()
	if first[0].p.Key != "soon" {
		t.Fatalf("first request went to %s", first[0].p.Key)
	}
	second, secondPlan := s.plan(*p, "m", provider.Chat)
	second, _, releaseSecond := burstSpread(p.Routing, nil, false, true, second, secondPlan)
	defer releaseSecond()
	if second[0].p.Key != "soon" {
		t.Fatalf("concurrent request went to %s while the earliest reset still had quota", second[0].p.Key)
	}
}

// Exercise the gateway's actual pool, affinity and request path, with quota
// supplied locally and a fake vendor that tells which key answered.
func TestResetPriorityRequests(t *testing.T) {
	for _, tc := range []struct {
		name, routing, order string
		want                 string
	}{
		{"listed", provider.Ordered, "", "b b b b b b"},
		{"rotate", provider.Rotate, "", "b c b c b c"},
		{"least used", provider.LeastUsed, "", "c c c c c c"},
		{"weighted", provider.Weighted, "", "b c b b c b"},
		{"soonest overrides rotation", provider.Rotate, "soonest", "c c c c c c"},
		{"smart", "", "", "c c c c c c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh(t)
			now := time.Now()
			keyWindows(t, map[string]provider.Allowance{
				"a": week(5, now.Add(6*24*time.Hour)),
				"b": week(50, now.Add(3*time.Hour)),
				"c": week(10, now.Add(time.Hour)),
			})
			var up keyed
			serveOn(t, "priority", "a", []string{"m"}, &up, "b", "c")
			p, err := provider.Find("priority")
			if err != nil {
				t.Fatal(err)
			}
			p.Routing = tc.routing
			p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week", Order: tc.order}
			p.Keys[0].Weight = 2 // b takes two for every one of c's
			if err := provider.Save(*p); err != nil {
				t.Fatal(err)
			}
			s := New()
			var got []string
			for i := range 6 {
				code, body := postAs(t, s, fmt.Sprintf("priority-%d", i), `{"model":"priority/m","messages":[{"role":"user","content":"hi"}]}`)
				if code != 200 {
					t.Fatalf("%d %s", code, body)
				}
				got = append(got, up.tried[len(up.tried)-1])
			}
			if strings.Join(got, " ") != tc.want {
				t.Fatalf("preferred requests = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestResetPriorityFailoverAndNoCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, window string
		fail         bool
		want         string
	}{
		{"one preferred account", "week", false, "b b"},
		{"preferred fails", "week", true, "b a a"},
		{"no selected cycle", "month", false, "a a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh(t)
			now := time.Now()
			keyWindows(t, map[string]provider.Allowance{"a": week(5, now.Add(5*24*time.Hour)), "b": week(20, now.Add(time.Hour))})
			var up keyed
			if tc.fail {
				up.fail = map[string]int{"b": 429}
			}
			serveOn(t, "priority", "a", []string{"m"}, &up, "b")
			p, _ := provider.Find("priority")
			p.Routing = provider.Rotate
			if tc.window == "month" {
				p.Routing = provider.Ordered
			}
			p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: tc.window}
			if err := provider.Save(*p); err != nil {
				t.Fatal(err)
			}
			s := New()
			for i := range 2 {
				if code, body := postAs(t, s, fmt.Sprintf("request-%d", i), `{"model":"priority/m","messages":[{"role":"user","content":"hi"}]}`); code != 200 {
					t.Fatalf("%d %s", code, body)
				}
			}
			if got := strings.Join(up.tried, " "); got != tc.want {
				t.Fatalf("tried %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResetPriorityNewTurnAndExplicitAffinity(t *testing.T) {
	for _, mode := range []string{"", provider.AffinityTurn, provider.AffinitySession, provider.AffinityStrict} {
		t.Run(mode, func(t *testing.T) {
			fresh(t)
			now := time.Now()
			windows := map[string]provider.Allowance{"a": week(10, now.Add(5*24*time.Hour)), "b": week(20, now.Add(6*24*time.Hour))}
			keyWindows(t, windows)
			up := &cacheWriter{wrote: 3000}
			vendor := httptest.NewServer(up)
			t.Cleanup(vendor.Close)
			var p provider.Provider
			p.ID, p.Name, p.Key = "priority", "Priority", "a"
			p.Keys = []provider.KeyAccount{{Key: "b"}}
			p.Models, p.Anthropic, p.Affinity = []string{"m"}, vendor.URL+"/v1", mode
			p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week"}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			s := New()
			t.Cleanup(s.subscription.abortAll)
			conv := `{"role":"user","content":"fix it"}`
			var got []string
			for i := range 3 {
				if i == 1 {
					windows["b"] = week(99, now.Add(time.Hour))
				}
				body := `{"model":"priority/m","max_tokens":100,"tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[` + conv + `]}`
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
				r.Header.Set("x-claude-code-session-id", "priority-session")
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				who := "a"
				if strings.Contains(w.Body.String(), "from b") {
					who = "b"
				}
				got = append(got, who)
				if i == 0 {
					conv += `,{"role":"assistant","content":"done"},{"role":"user","content":"more"}`
				} else {
					conv += `,{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}`
				}
			}
			want := "a b b"
			if mode == provider.AffinitySession || mode == provider.AffinityStrict {
				want = "a a a"
			}
			if strings.Join(got, " ") != want {
				t.Fatalf("turns and tool loop used %v, want %s", got, want)
			}
		})
	}
}

func TestResetPrioritySurvivesGroupRouting(t *testing.T) {
	for _, outer := range []string{"", provider.Rotate, provider.LeastUsed, provider.Ordered} {
		t.Run(outer, func(t *testing.T) {
			fresh(t)
			now := time.Now()
			keyWindows(t, map[string]provider.Allowance{"a": week(5, now.Add(5*24*time.Hour)), "b": week(20, now.Add(time.Hour))})
			var up keyed
			serveOn(t, "priority", "a", []string{"m"}, &up, "b")
			p, _ := provider.Find("priority")
			p.ResetPriority = &provider.ResetPriority{Hours: 4, Window: "week"}
			if err := provider.Save(*p); err != nil {
				t.Fatal(err)
			}
			if err := provider.SaveGroup(provider.Group{ID: "pool", Name: "Pool", Members: []string{"priority/m"}, Routing: outer}); err != nil {
				t.Fatal(err)
			}
			s := New()
			for i := range 3 {
				if code, body := postAs(t, s, fmt.Sprintf("group-%d", i), `{"model":"group/pool","messages":[{"role":"user","content":"hi"}]}`); code != 200 {
					t.Fatalf("%d %s", code, body)
				}
			}
			if strings.Join(up.tried, " ") != "b b b" {
				t.Fatalf("outer routing %q escaped preferred account: %v", outer, up.tried)
			}
		})
	}
}
