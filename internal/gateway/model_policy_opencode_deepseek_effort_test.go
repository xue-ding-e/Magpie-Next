package gateway

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/usage"
)

// The profile fills in its default after translation. The request still
// records what was asked, while each try and usage record name what went.
func TestOpenCodeDeepSeekEffortRecordedAsSent(t *testing.T) {
	for _, proto := range []string{"chat", "responses", "messages"} {
		for _, effort := range []string{"", "low", "none"} {
			t.Run(proto+"/"+effort, func(t *testing.T) {
				s, captured := dsProfileServer(t, chatSSE("EFFORT_OK", "", ""))
				// An upstream offering none keeps the caller's explicit off,
				// rather than fitting it to a model that cannot stop thinking.
				if err := provider.SetModelEfforts("ds/"+dsCapturedModel, []string{"none", "low", "max"}); err != nil {
					t.Fatal(err)
				}
				path, content := "/v1/chat/completions", `"messages":[{"role":"user","content":"hi"}]`
				reasoning := ""
				switch proto {
				case "responses":
					path, content = "/v1/responses", `"input":"hi"`
					if effort != "" {
						reasoning = fmt.Sprintf(`,"reasoning":{"effort":%q}`, effort)
					}
				case "messages":
					path = "/v1/messages"
					content += `,"max_tokens":4096`
					if effort == "none" {
						reasoning = `,"thinking":{"type":"disabled"}`
					} else if effort != "" {
						reasoning = fmt.Sprintf(`,"thinking":{"type":"adaptive"},"output_config":{"effort":%q}`, effort)
					}
				default:
					if effort != "" {
						reasoning = fmt.Sprintf(`,"reasoning_effort":%q`, effort)
					}
				}
				body := fmt.Sprintf(`{"model":"ds/%s",%s%s}`, dsCapturedModel, content, reasoning)
				zenSend(t, s, path, body)
				want := effort
				if want == "" {
					want = "max"
				}
				got := captured()
				if len(got) != 1 || got[0].body["reasoning_effort"] != want {
					t.Fatalf("wire effort: %+v, want %q", got, want)
				}
				route := lastRoute(s)
				asked := effort
				if proto == "messages" && effort == "none" {
					asked = "" // disabled thinking carries no reasoning level
				}
				if route.Effort != asked || len(route.Tries) != 1 || route.Tries[0].Effort != want {
					t.Errorf("route asked %q, tries %+v; want asked %q, sent %q", route.Effort, route.Tries, asked, want)
				}
				records := usage.Load(time.Time{})
				if len(records) != 1 || records[0].Effort != want {
					t.Errorf("usage records %+v, want sent effort %q", records, want)
				}
			})
		}
	}
}

func TestOpenCodeDeepSeekProviderEffortRecordedAsSent(t *testing.T) {
	s, captured := dsProfileServer(t, chatSSE("EXTRA_EFFORT_OK", "", ""))
	p, err := provider.Find("ds")
	if err != nil {
		t.Fatal(err)
	}
	p.ExtraBody = provider.ExtraBody{"reasoning_effort": json.RawMessage(`"high"`)}
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	zenSend(t, s, "/v1/chat/completions", `{"model":"ds/deepseek-v4.1-flash-free","messages":[{"role":"user","content":"hi"}]}`)
	got := captured()
	if len(got) != 1 || got[0].body["reasoning_effort"] != "high" {
		t.Fatalf("provider effort was replaced by the profile default: %+v", got)
	}
	route := lastRoute(s)
	if len(route.Tries) != 1 || route.Tries[0].Effort != "high" {
		t.Errorf("provider effort missing from try: %+v", route.Tries)
	}
	if records := usage.Load(time.Time{}); len(records) != 1 || records[0].Effort != "high" {
		t.Errorf("provider effort missing from usage: %+v", records)
	}
}
