//go:build next

package update

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

type nextRejectTransport struct{ calls int }

func (tr *nextRejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	tr.calls++
	panic("Next must not contact an upstream or environment-supplied update feed")
}

func TestNextDistributionNeverFetchesUpdates(t *testing.T) {
	old := client
	tr := &nextRejectTransport{}
	client = &http.Client{Transport: tr}
	t.Cleanup(func() { client = old })
	t.Setenv("MAGPIE_UPDATE_FEED", "https://example.invalid/upstream")
	for _, lang := range []string{"", "zh", "en"} {
		release, err := LatestIn(context.Background(), lang)
		if release != nil || err == nil || !strings.Contains(err.Error(), "xue-ding-e/Magpie-Next/releases") {
			t.Fatalf("unexpected manual-update result: %v, %v", release, err)
		}
	}
	if tr.calls != 0 || Released("0.1.0") || Released("0.1.0-next.1") {
		t.Fatal("Next distribution must not self-update or fetch upstream metadata")
	}
}
