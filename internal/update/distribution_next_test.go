//go:build next

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/update/nextrelease"
)

type nextTestTransport func(*http.Request) (*http.Response, error)

func (f nextTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type nextFixture struct {
	mu       sync.Mutex
	sequence uint64
	sha      string
	base     string
	gui, cli []byte
	corrupt  bool
	legacy   bool
	noDigest bool
	seen     []string
}

func nextHash(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func nextServer(t *testing.T) (*nextFixture, context.Context) {
	t.Helper()
	var f = &nextFixture{sequence: 41, sha: strings.Repeat("a", 40), base: "0.1.1311", gui: []byte("next GUI"), cli: []byte("next CLI")}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	old := client
	// This synthetic HTTP proxy answers every request. Preserve the native
	// proxy dispatcher and context, changing HTTPS to HTTP only inside the test
	// transport so the fixture can answer without a real CONNECT/TLS endpoint.
	client = &http.Client{Transport: nextTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || (req.URL.Host != "api.github.com" && req.URL.Host != "github.com" && req.URL.Host != "release-assets.githubusercontent.com" && req.URL.Host != "mirror.invalid") {
			return nil, fmt.Errorf("unexpected Next destination before DNS: %s", req.URL.Host)
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			return nil, errors.New("Next sent credentials")
		}
		copy := req.Clone(req.Context())
		copy.URL.Scheme = "http"
		return old.Transport.RoundTrip(copy)
	})}
	t.Cleanup(func() { client = old })
	statePath := filepath.Join(t.TempDir(), "next-state.json")
	oldPath := nextStatePath
	nextStatePath = func() string { return statePath }
	t.Cleanup(func() { nextStatePath = oldPath })
	t.Setenv("MAGPIE_UPDATE_FEED", "https://example.invalid/upstream")
	t.Setenv("MAGPIE_NOTES_FEED", "https://example.invalid/upstream-notes")
	t.Setenv("MAGPIE_MIRRORS", "off")
	return f, WithMirror(WithProxy(context.Background(), server.URL), "off")
}

func (f *nextFixture) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, req.URL.String())
	const api = "https://api.github.com/repos/xue-ding-e/Magpie-Next"
	const releases = "https://github.com/xue-ding-e/Magpie-Next/releases"
	tag := "next-" + f.sha[:12]
	version := fmt.Sprintf("%s-next.%d", f.base, f.sequence)
	var manifest = nextrelease.Manifest{Schema: 1, Repository: "xue-ding-e/Magpie-Next", Channel: "stable", Sequence: f.sequence,
		Version: version, SourceSHA: f.sha, Platform: "windows-amd64", Integrity: "sha256-unsigned",
		Assets: []nextrelease.Asset{{Name: "Magpie-Next.exe", Size: int64(len(f.gui)), SHA256: nextHash(f.gui)}, {Name: "magpie-next-cli.exe", Size: int64(len(f.cli)), SHA256: nextHash(f.cli)}}}
	raw, _ := json.Marshal(manifest)
	var assets []map[string]any
	for i, name := range []string{"manifest.json", "Magpie-Next.exe", "magpie-next-cli.exe"} {
		body := raw
		if i == 1 {
			body = f.gui
		}
		if i == 2 {
			body = f.cli
		}
		var asset = map[string]any{"id": 70 + i, "name": name, "url": api + "/releases/assets/" + strconv.Itoa(70+i), "state": "uploaded", "size": len(body), "browser_download_url": releases + "/download/" + tag + "/" + name}
		if !f.noDigest {
			asset["digest"] = "sha256:" + nextHash(body)
		}
		assets = append(assets, asset)
	}
	if f.legacy {
		assets = []map[string]any{{"id": 70, "name": "portable.zip", "url": api + "/releases/assets/70", "state": "uploaded", "size": 1, "browser_download_url": releases + "/download/" + tag + "/portable.zip"}}
	}
	var release = map[string]any{"id": 7, "url": api + "/releases/7", "tag_name": tag, "name": "Magpie Next " + version, "draft": false, "prerelease": false, "body": "Next changes\n<!-- lang:zh -->\nNext 更新", "assets": assets}
	switch req.URL.Path {
	case "/repos/xue-ding-e/Magpie-Next/releases/latest", "/repos/xue-ding-e/Magpie-Next/releases/7":
		json.NewEncoder(w).Encode(release)
	case "/repos/xue-ding-e/Magpie-Next/releases":
		if req.URL.Query().Get("page") == "1" {
			json.NewEncoder(w).Encode([]any{release})
		} else {
			fmt.Fprint(w, "[]")
		}
	case "/repos/xue-ding-e/Magpie-Next/git/ref/tags/" + tag:
		json.NewEncoder(w).Encode(map[string]any{"ref": "refs/tags/" + tag, "url": api + "/git/refs/tags/" + tag, "object": map[string]any{"type": "commit", "sha": f.sha, "url": api + "/git/commits/" + f.sha}})
	case "/repos/xue-ding-e/Magpie-Next/git/commits/" + f.sha:
		json.NewEncoder(w).Encode(map[string]any{"sha": f.sha, "url": api + "/git/commits/" + f.sha})
	case "/repos/xue-ding-e/Magpie-Next/releases/assets/70":
		http.Redirect(w, req, "https://release-assets.githubusercontent.com/synthetic/manifest.json", http.StatusFound)
	case "/synthetic/manifest.json":
		w.Write(raw)
	case "/synthetic/Magpie-Next.exe", "/synthetic/magpie-next-cli.exe":
		body := f.cli
		if strings.HasSuffix(req.URL.Path, "/Magpie-Next.exe") {
			body = f.gui
		}
		if f.corrupt {
			body = []byte("corrupted download")
		}
		w.Write(body)
	default:
		if strings.HasSuffix(req.URL.Path, "/Magpie-Next.exe") || strings.HasSuffix(req.URL.Path, "/magpie-next-cli.exe") {
			name := filepath.Base(req.URL.Path)
			http.Redirect(w, req, "https://release-assets.githubusercontent.com/synthetic/"+name, http.StatusFound)
		} else {
			http.Error(w, "unknown synthetic route", http.StatusNotFound)
		}
	}
}

// The native CLI and GUI entry points use Latest/In, Newer and StageBinary.
// Keep that same path through both asset kinds, actual hashing, native install
// and restart rollback; only OS process creation is replaced by a test double.
func TestNextNativeUpdatePipeline(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("published platform is Windows x64")
	}
	f, ctx := nextServer(t)
	oldGUI, oldTrial, oldLaunch := GUI, canStart, launch
	t.Cleanup(func() { GUI, canStart, launch = oldGUI, oldTrial, oldLaunch })
	trialCalls, launches := 0, 0
	refuseRestart := false
	canStart = func(string) error { trialCalls++; return nil }
	launch = func(exe string, args, env []string) (<-chan struct{}, func(), error) {
		if refuseRestart {
			return nil, nil, errors.New("synthetic restart refusal")
		}
		launches++
		if strings.Join(args, " ") != "gui" {
			t.Errorf("wrong restart args: %v", args)
		}
		for _, value := range env {
			if marker, ok := strings.CutPrefix(value, startedEnv+"="); ok {
				if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
					return nil, nil, err
				}
			}
		}
		return make(chan struct{}), func() {}, nil
	}
	for _, gui := range []bool{false, true} {
		GUI = gui
		rel, err := LatestIn(ctx, "zh")
		if err != nil {
			t.Fatal(err)
		}
		if rel.Version != "0.1.1311-next.41" || rel.Notes != "Next 更新" || !Released(rel.Version) || !Newer(rel.Version, "0.1.1311-next.40") || Newer(rel.Version, rel.Version) {
			t.Fatalf("native update is not offered correctly: %+v", rel)
		}
		if rel.URL != Site+"/tag/next-aaaaaaaaaaaa" {
			t.Fatal("wrong release destination", rel.URL)
		}
		want, name := f.cli, "magpie-next-cli.exe"
		if gui {
			want, name = f.gui, "Magpie-Next.exe"
		}
		if BinaryAsset() != name || rel.Assets[name].SHA256 != nextHash(want) {
			t.Fatal("wrong native asset", BinaryAsset(), rel.Assets)
		}
		var done, total int64
		staged, err := StageBinary(WithProgress(ctx, func(d, n int64) { done, total = d, n }), rel)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(staged) })
		if got, _ := os.ReadFile(staged); string(got) != string(want) || done != int64(len(want)) || total != int64(len(want)) {
			t.Fatal("wrong staged executable or progress", string(got), done, total)
		}
		exe := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(exe, []byte("old installed Next"), 0o755); err != nil {
			t.Fatal(err)
		}
		old, err := InstallBinaryAside(staged, exe)
		if err != nil || old == "" {
			t.Fatal("native installation failed", old, err)
		}
		if got, _ := os.ReadFile(exe); string(got) != string(want) {
			t.Fatal("download was not installed")
		}
		if err := RelaunchBinary(exe, old, true, ""); err != nil {
			t.Fatal("native restart failed", err)
		}
		refuseRestart = true
		if err := RelaunchBinary(exe, old, true, ""); err == nil {
			t.Fatal("failed restart did not report failure")
		}
		if got, _ := os.ReadFile(exe); string(got) != "old installed Next" {
			t.Fatal("native restart did not roll back")
		}
		// Installation failure leaves the already accepted release retryable.
		if retry, err := Latest(ctx); err != nil || retry.Version != rel.Version {
			t.Fatal("failed installation cannot retry", retry, err)
		}
		refuseRestart = false
	}
	if trialCalls != 2 || launches != 2 {
		t.Fatal("native trial/restart path was bypassed", trialCalls, launches)
	}
	f.mu.Lock()
	f.sequence, f.sha, f.base = 42, strings.Repeat("b", 40), "0.1.1200"
	f.mu.Unlock()
	next, err := Latest(ctx)
	if err != nil || !Newer(next.Version, "0.1.1311-next.41") || Newer("0.9.9999-next.41", next.Version) {
		t.Fatal("next own version is not ordered by manifest sequence", next, err)
	}
	notes, err := NotesBetween(ctx, "0.1.1311-next.41", next.Version, "zh")
	if err != nil || len(notes) != 1 || notes[0].Version != next.Version || notes[0].Notes != "Next 更新" {
		t.Fatal("Next notes left the own repository", notes, err)
	}
	if Feed() != nextFeed || NotesFeed() != nextFeed || ReleasePage(next.Version) != Site {
		t.Fatal("environment override reached Next metadata")
	}
}

func TestNextHighWaterAndDownloadVerification(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("published platform is Windows x64")
	}
	f, ctx := nextServer(t)
	f.mu.Lock()
	f.noDigest = true
	f.mu.Unlock()
	rel, err := Latest(ctx)
	if err != nil {
		t.Fatal("GitHub's optional digest must not prevent native updates", err)
	}
	// The update mirror changes only executable downloads. Metadata and the
	// manifest are still checked against the fixed own GitHub API and source tag.
	asset := rel.Assets["Magpie-Next.exe"]
	path := filepath.Join(t.TempDir(), "next.new")
	if err := download(WithMirror(ctx, "https://mirror.invalid/"), asset, path); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.corrupt = true
	f.mu.Unlock()
	// The upstream downloader now reuses a complete, already verified file.
	// Start with no cached file so this check actually receives corrupt bytes.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := fetch(ctx, asset, path); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("corrupt executable was accepted", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("bad executable was kept", err)
	}
	f.mu.Lock()
	f.corrupt = false
	f.mu.Unlock()
	if retry, err := Latest(ctx); err != nil || retry.Version != rel.Version {
		t.Fatal("hash failure made the same release unavailable", retry, err)
	}
	accepted, err := os.ReadFile(nextStatePath())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"lower sequence", "same sequence new source", "same release changed hash"} {
		f.mu.Lock()
		f.sequence, f.sha, f.gui, f.base = 41, strings.Repeat("a", 40), []byte("next GUI"), "0.1.1311"
		switch mode {
		case "lower sequence":
			f.sequence, f.sha, f.base = 40, strings.Repeat("b", 40), "9.9.9"
		case "same sequence new source":
			f.sha = strings.Repeat("c", 40)
		case "same release changed hash":
			f.gui = []byte("tampered executable")
		}
		f.mu.Unlock()
		if got, err := Latest(ctx); err == nil || got != nil {
			t.Fatal(mode, "was accepted", got, err)
		}
		if got, err := os.ReadFile(nextStatePath()); err != nil || string(got) != string(accepted) {
			t.Fatal(mode, "lowered or changed persisted high-water state", err)
		}
	}
	if err := os.WriteFile(nextStatePath(), []byte("unreadable JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Latest(ctx); err == nil || got != nil {
		t.Fatal("bad state read became an empty state", got, err)
	}
}

func TestNextLegacyZIPIsManualOnly(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("published platform is Windows x64")
	}
	f, ctx := nextServer(t)
	f.mu.Lock()
	f.legacy = true
	f.mu.Unlock()
	if got, err := Latest(ctx); err == nil || got != nil || !strings.Contains(err.Error(), "manual") {
		t.Fatal("ZIP-only release became an update", got, err)
	}
	if _, err := os.Stat(nextStatePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy release initialized accepted state", err)
	}
	if Released("next-aaaaaaaaaaaa") || Released("dev") || Newer("0.1.1311-next.2", "next-aaaaaaaaaaaa") {
		t.Fatal("legacy/source build entered native updater")
	}
}

func TestNextConcurrentChecksKeepAcceptedIdentity(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("published platform is Windows x64")
	}
	_, ctx := nextServer(t)
	var checks sync.WaitGroup
	var failures = make(chan error, 4)
	for range 4 {
		checks.Go(func() {
			release, err := Latest(ctx)
			if err != nil {
				failures <- err
				return
			}
			if release.Version != "0.1.1311-next.41" {
				failures <- fmt.Errorf("wrong concurrent version: %s", release.Version)
			}
		})
	}
	checks.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var accepted nextrelease.Identity
	raw, err := os.ReadFile(nextStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &accepted); err != nil || accepted.Sequence != 41 || accepted.Version != "0.1.1311-next.41" {
		t.Fatal("concurrent checks lost accepted state", accepted, err)
	}
}
