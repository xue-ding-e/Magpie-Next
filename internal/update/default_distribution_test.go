package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This test is deliberately untagged: plain go build/go test must select the
// fork's distribution, rather than relying on a package workflow's flags.
func TestDefaultDistributionIsNext(t *testing.T) {
	if !nextDistribution || !strings.HasSuffix(Site, "/Magpie-Next/releases") {
		t.Fatalf("default source build chose another distribution: %s", Site)
	}
	for _, override := range []string{
		"https://example.invalid/upstream",
		"http://127.0.0.1:12345/fixture",
		"http://feed.magpie.invalid/api/latest",
	} {
		t.Setenv("MAGPIE_UPDATE_FEED", override)
		t.Setenv("MAGPIE_NOTES_FEED", override)
		if Feed() != nextFeed || NotesFeed() != nextFeed || !strings.Contains(Feed(), "/Magpie-Next/releases/latest") {
			t.Fatalf("ambient variables changed the declared source: %s / %s", Feed(), NotesFeed())
		}
		if ReleasePage("v9.9.9") != Site {
			t.Fatal("the release link left the own repository")
		}
	}
}

func TestFixtureFeedScope(t *testing.T) {
	for _, c := range []struct {
		value string
		want  bool
	}{
		{"http://127.0.0.1:12345/feed", true},
		{"http://[::1]:12345/feed", true},
		{"http://feed.magpie.invalid/api/latest", true},
		{"https://example.invalid/feed", false},
		{"http://192.0.2.1/feed", false},
		{"http://name:password@localhost/feed", false},
		{"http://localhost/feed#fragment", false},
	} {
		t.Setenv("MAGPIE_UPDATE_FEED", c.value)
		if got := fixtureFeed("MAGPIE_UPDATE_FEED"); (got != "") != c.want {
			t.Fatalf("fixture accepted=%v for %s, want %v", got != "", c.value, c.want)
		}
	}
}

func TestFixtureFeedRequiresSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("checking the fixture scope must not make a request")
	}))
	t.Cleanup(srv.Close)
	for _, key := range []string{"MAGPIE_UPDATE_FEED", "MAGPIE_NOTES_FEED"} {
		t.Setenv(key, srv.URL)
		if got := fixtureFeed(key); got != srv.URL {
			t.Fatalf("isolated loopback fixture refused for %s: %q", key, got)
		}
	}
	for _, marker := range []string{"", "relative-sandbox", filepath.Join(t.TempDir(), "missing")} {
		t.Run(marker, func(t *testing.T) {
			t.Setenv("MAGPIE_TEST_SANDBOX", marker)
			for _, key := range []string{"MAGPIE_UPDATE_FEED", "MAGPIE_NOTES_FEED"} {
				if got := fixtureFeed(key); got != "" {
					t.Fatalf("fixture %s accepted without a valid sandbox: %q", key, got)
				}
			}
		})
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "relative-root")
			if got := fixtureFeed("MAGPIE_UPDATE_FEED"); got != "" {
				t.Fatalf("fixture accepted an invalid %s: %q", key, got)
			}
		})
	}
	if fixtureFeed("OTHER_FEED") != "" {
		t.Fatal("unrecognized fixture variable accepted")
	}
}

// GUI transport fixtures use the current platform's asset name even where
// Next publishes no build. Their native staging still checks the real hash.
func TestStageBinaryAllowsIsolatedFixtureAcrossPlatforms(t *testing.T) {
	body := []byte("isolated native update fixture")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MAGPIE_UPDATE_FEED", srv.URL)
	release := &Release{Version: "0.1.11", Assets: map[string]Asset{
		BinaryAsset(): {URL: srv.URL + "/asset", SHA256: hex.EncodeToString(sum[:])},
	}}
	staged, err := StageBinary(context.Background(), release)
	if err != nil {
		t.Fatalf("isolated fixture could not stage on %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	t.Cleanup(func() { os.Remove(staged) })
	got, err := os.ReadFile(staged)
	if err != nil || string(got) != string(body) {
		t.Fatalf("staged fixture = %q, %v", got, err)
	}
	if Feed() != nextFeed || NotesFeed() != nextFeed {
		t.Fatal("staging a test fixture changed the declared distribution")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		for _, override := range []string{"", "http://192.0.2.1/feed"} {
			t.Setenv("MAGPIE_UPDATE_FEED", override)
			if _, err := StageBinary(context.Background(), release); err == nil || !strings.Contains(err.Error(), "Windows x64 only") {
				t.Fatalf("unsupported native install accepted with feed %q: %v", override, err)
			}
		}
	}
}
