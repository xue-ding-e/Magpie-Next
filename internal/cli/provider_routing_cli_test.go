package cli

import (
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// routing= on a provider spreads requests over its keys or accounts as
// the editor's Routing does: fast (fastest, speed) is Speed first; manual
// and cheapest are a group's alone, and a name magpie doesn't know is refused.
func TestProviderRoutingPair(t *testing.T) {
	var p provider.Provider
	for in, want := range map[string]string{"fast": provider.Fastest, "fastest": provider.Fastest, "Speed": provider.Fastest, "order": provider.Ordered, "smart": ""} {
		if err := applyPairs(&p, []string{"routing=" + in}); err != nil || p.Routing != want {
			t.Errorf("routing=%s: %q %v, want %q", in, p.Routing, err, want)
		}
	}
	p.Routing = provider.Fastest
	for _, bad := range []string{"manual", "cheapest"} {
		if err := applyPairs(&p, []string{"routing=" + bad}); err == nil || p.Routing != provider.Fastest {
			t.Errorf("routing=%s taken: %q", bad, p.Routing)
		}
	}
}

func TestProviderResetPriorityPair(t *testing.T) {
	var p provider.Provider
	if err := applyPairs(&p, []string{"reset-priority=24h,week,soonest"}); err != nil || p.ResetPriority == nil || p.ResetPriority.Hours != 24 || p.ResetPriority.Order != "soonest" {
		t.Fatalf("priority not applied: %+v %v", p.ResetPriority, err)
	}
	if err := applyPairs(&p, []string{"reset-priority=bad"}); err == nil || p.ResetPriority == nil || p.ResetPriority.Hours != 24 {
		t.Fatal("invalid priority changed the saved choice")
	}
	if err := applyPairs(&p, []string{"reset-priority=off"}); err != nil || p.ResetPriority != nil {
		t.Fatalf("priority not cleared: %+v %v", p.ResetPriority, err)
	}
}
