package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

// linkaww on Discord, a magpie in Docker in gateway mode: Usage › Sessions
// was all but empty. Its list is the sessions whose keys the stats name
// (st.keys), and those were only the sessions read from this computer's
// files: one that came through the gateway alone, which in gateway mode
// is every one, was counted in the totals and never listed.
func TestGatewayModeStatsNameTheGatewaySessions(t *testing.T) {
	sandboxHome(t)
	// These files are absent on a new install. On Windows, steady.Stat
	// waits out a possible rename of a missing file whose folder exists.
	// Give this stats test settled inputs so its clock measures the query,
	// rather than those deliberate filesystem retries.
	if err := os.MkdirAll(filepath.Dir(settings.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"settings.json":         `{}`,
		"providers.json":        `[]`,
		"logins.json":           `[]`,
		"plugin-providers.json": `[]`,
		"plugin-searches.json":  `{}`,
		"stash.json":            `{}`,
		"library.json":          `{}`,
	} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(settings.Path()), name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Process discovery keeps directory-probe channels, and reading a
	// ledger chunk arms a scratch timer. Initialize them outside the
	// bubble: neither may leave a bubble-owned primitive for the next
	// test. The worker finishes before the first stats query, and is
	// joined even if the bubble fails before handing it the fixture.
	fixture := make(chan usage.Record)
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		if r, ok := <-fixture; ok {
			usage.Append(r)
			provider.All()
			agent.Looks()
			usage.GatewaySessions(time.Time{}, nil)
		}
	}()
	defer func() { close(fixture); <-ready }()
	synctest.Test(t, func(t *testing.T) {
		now := time.Now().In(time.Local)
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		if !now.Before(midnight.Add(30 * time.Minute)) {
			midnight = midnight.AddDate(0, 0, 1)
		}
		time.Sleep(midnight.Add(30 * time.Minute).Sub(now))
		sessions.Reset()
		forgetStats()
		t.Cleanup(func() {
			synctest.Wait()
			webPage.Store(false)
			ownWas.Store(0)
			sessions.Reset()
			forgetStats()
		})
		const remote = "8c1f0e7a-1111-4222-8333-444455556666"
		at := time.Now()
		fixture <- usage.Record{Time: at, Agent: "claude", Session: remote, Provider: "fake", Model: "m", Input: 50, Output: 5, Status: 200}
		<-ready

		mux := http.NewServeMux()
		sessionRoutes(mux, folderOnly{})
		gatewayModeRoutes(mux)
		ok := func(method, path, body string) []byte {
			t.Helper()
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
			if w.Code != 200 {
				t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
			}
			return w.Body.Bytes()
		}
		keys := func(days string) []string {
			t.Helper()
			var st struct{ Keys []string }
			if err := json.Unmarshal(ok("GET", "/api/sessions/stats?days="+days, ""), &st); err != nil {
				t.Fatal(err)
			}
			return st.Keys
		}
		for _, mode := range []string{"off", "on"} {
			if mode == "on" {
				webPage.Store(true)
				ok("POST", "/api/settings/gateway-mode", `{"mode":"on"}`)
			}
			for _, days := range []string{"0", "7", "1"} {
				if k := keys(days); !slices.Contains(k, "claude:"+remote) {
					t.Fatalf("gateway mode %s, days=%s: keys %v leave out the gateway's session", mode, days, k)
				}
				if now := time.Now(); !now.Equal(at) {
					t.Fatalf("virtual clock advanced during stats query: %v to %v", at, now)
				}
			}
		}
	})
}
