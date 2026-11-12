package gui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/filememo"
	"github.com/yetone/magpie/internal/provider"
)

// A provider's row on the Providers page reads the vendor's list once:
// it was read four times (the picks, what is exposed twice, the newest
// of each family), each built whole with its names and levels, for each
// provider on every poll. What the row says is the same.
func TestProviderInfoReadsTheListOnce(t *testing.T) {
	h := sandboxHome(t)
	var b strings.Builder
	b.WriteString(`{"relayco": {"models": {`)
	for i := range 2000 {
		fmt.Fprintf(&b, `"m-%d": {"id":"m-%d","name":"M %d"},`, i, i, i)
	}
	b.WriteString(`"x": {"id":"x"}}}}`)
	os.MkdirAll(h+"/.cache/magpie", 0o755)
	if err := os.WriteFile(catalog.CachePath(), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog.Reset()
	t.Cleanup(catalog.Reset)
	p := provider.Provider{ID: "r", Name: "R", Key: "k", Chat: "http://127.0.0.1:1/v1", Catalog: "relayco", Models: []string{"m-3", "m-1", "gone"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(provider.Path(), old, old)
	os.Chtimes(catalog.CachePath(), old, old)
	if len(p.Available()) < 2000 {
		t.Fatalf("the catalog's models not listed: %d", len(p.Available()))
	}

	// as a request to the page reads it (held)
	defer provider.Hold()()
	defer filememo.Hold()()
	// Count real list builds. On Windows a row's per-model environment
	// and file lookups can cost more than several complete lists, and
	// TotalAlloc also includes work done by unrelated goroutines.
	list := providerInfoList
	builds := 0
	providerInfoList = func(p provider.Provider) provider.Listing {
		builds++
		return list(p)
	}
	t.Cleanup(func() { providerInfoList = list })
	got := providerInfo(p, nil)
	if builds != 1 {
		t.Fatalf("a provider row built its model list %d times, want once", builds)
	}
	if !slices.Equal(got.Chosen, p.Picks()) {
		t.Fatalf("chosen %v, picks %v", got.Chosen, p.Picks())
	}
	if got.Exposed != len(p.Exposed()) || got.Models[0].ID != "gone" {
		t.Fatalf("exposed %d of %d, first %+v", got.Exposed, len(p.Exposed()), got.Models[0])
	}
}
