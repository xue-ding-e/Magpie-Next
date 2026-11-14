package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

// An explicit gateway pick reaches a Codex thread already running at an
// older level. Ultra stays the pick; the native API receives its max.
func TestCodexNativeGatewayEffort(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, pick, reasoning, want string
	}{
		{"medium", "POST", "/responses", "ultra", `,"reasoning":{"effort":"medium","summary":"detailed","context":"all_turns"}`, "max"},
		{"xhigh", "POST", "/responses", "ultra", `,"reasoning":{"effort":"xhigh","summary":"auto"}`, "max"},
		{"missing", "POST", "/responses", "ultra", "", "max"},
		{"high", "POST", "/responses", "high", `,"reasoning":{"effort":"medium"}`, "high"},
		{"cleared", "POST", "/responses", "", `,"reasoning":{"effort":"medium"}`, "medium"},
		{"get", "GET", "/responses", "ultra", `,"reasoning":{"effort":"medium"}`, "medium"},
		{"compact", "POST", "/responses/compact", "ultra", `,"reasoning":{"effort":"medium"}`, "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t, provider.Chat, &fake{t: t})
			got := make(chan []byte, 1)
			chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				got <- b
				if strings.HasSuffix(r.URL.Path, "/compact") {
					io.WriteString(w, `{"id":"c1","object":"response.compaction","output":[]}`)
					return
				}
				w.Header()["Content-Type"] = nil
				io.WriteString(w, sse(`data: {"type":"response.completed","response":{"id":"r1","model":"gpt-6.1-sol","usage":{"input_tokens":9,"output_tokens":2}}}`))
			})
			// Seed the persisted setting independently of its setter so the
			// native routing regression also fails before Ultra is accepted.
			set := settings.Load()
			set.AgentEfforts = map[string]string{"codex": tc.pick}
			if err := settings.Save(set); err != nil {
				t.Fatal(err)
			}
			provider.Changed()
			body := `{"model":"gpt-6.1-sol","stream":true,"input":"hi"` + tc.reasoning + `}`
			s := New()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, CodexPath+tc.path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer chatgpt-token")
			r.Header.Set("User-Agent", "Codex Desktop/0.162.3")
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("native request: %d %s", w.Code, w.Body)
			}
			var sent struct {
				Model     string            `json:"model"`
				Reasoning map[string]string `json:"reasoning"`
			}
			if err := json.Unmarshal(<-got, &sent); err != nil {
				t.Fatal(err)
			}
			if sent.Model != "gpt-6.1-sol" || sent.Reasoning["effort"] != tc.want {
				t.Fatalf("native backend received model %q, reasoning %v; want gpt-6.1-sol at %s", sent.Model, sent.Reasoning, tc.want)
			}
			var original struct {
				Reasoning map[string]string `json:"reasoning"`
			}
			json.Unmarshal([]byte(body), &original)
			for key, value := range original.Reasoning {
				if key != "effort" && sent.Reasoning[key] != value {
					t.Errorf("reasoning.%s = %q, want %q", key, sent.Reasoning[key], value)
				}
			}
			if kept := provider.AgentEffort("codex"); kept != tc.pick {
				t.Fatalf("gateway pick changed from %q to %q", tc.pick, kept)
			}
			if tc.path == "/responses" && tc.method == "POST" {
				if recs := usage.Load(time.Time{}); len(recs) != 1 || recs[0].Effort != tc.want {
					t.Errorf("usage must name the level actually sent: %+v", recs)
				}
				if routes := s.Trace(t.Context(), 0, 0).Routes; len(routes) != 1 || routes[0].Effort != tc.want || len(routes[0].Tries) != 1 || routes[0].Tries[0].Effort != tc.want {
					t.Errorf("trace must name the level actually sent: %+v", routes)
				}
			}
		})
	}
}

func TestCodexNativeGatewayEffortClearedLive(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	got := make(chan []byte, 1)
	chatgpt(t, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		got <- b
		w.Header()["Content-Type"] = nil
		io.WriteString(w, sse(`data: {"type":"response.completed","response":{"id":"r1","usage":{"input_tokens":9,"output_tokens":2}}}`))
	})
	s := New()
	for _, tc := range []struct{ pick, want string }{{"ultra", "max"}, {"high", "high"}, {"", "medium"}} {
		if err := provider.SetAgentEffort("codex", tc.pick); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-6.1-sol","stream":true,"input":"hi","reasoning":{"effort":"medium"}}`))
		r.Header.Set("Authorization", "Bearer chatgpt-token")
		r.Header.Set("User-Agent", "Codex Desktop/0.162.3")
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("pick %q: %d %s", tc.pick, w.Code, w.Body)
		}
		if sent := bodyEffort(provider.Responses, <-got); sent != tc.want {
			t.Fatalf("pick %q on the same gateway sent %q, want %q", tc.pick, sent, tc.want)
		}
	}
}
