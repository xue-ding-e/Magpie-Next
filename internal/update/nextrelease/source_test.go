package nextrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func fixture(t *testing.T, sha string, seq uint64, preview bool) Record {
	t.Helper()
	channel := "stable"
	if preview {
		channel = "preview"
	}
	var m = Manifest{Schema: 1, Repository: repository, Channel: channel, Sequence: seq,
		Version: tagFor(sha), SourceSHA: sha, Platform: "windows-amd64", Integrity: "sha256-unsigned",
		Assets: []Asset{{Name: "Magpie-Next.exe", Size: 4, SHA256: strings.Repeat("a", 64)},
			{Name: "magpie-next-cli.exe", Size: 8, SHA256: strings.Repeat("b", 64)}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var r = Record{Tag: m.Version, CommitSHA: sha, Prerelease: preview, Manifest: raw}
	r.Assets = append(r.Assets, PublishedAsset{Name: "manifest.json", Size: int64(len(raw)), URL: downloadBase + r.Tag + "/manifest.json"})
	for _, a := range m.Assets {
		r.Assets = append(r.Assets, PublishedAsset{Name: a.Name, Size: a.Size, URL: downloadBase + r.Tag + "/" + a.Name})
	}
	return r
}

func legacyState() State {
	var s State
	s.ReadOK = true
	s.InstalledSHA = "d65e4026f0856215a35232fabc4cbb3f98fa52d7"
	s.InstalledVersion = tagFor(s.InstalledSHA)
	return s
}

func onePage(records ...Record) Page {
	return func(context.Context, string) ([]Record, bool, error) { return records, false, nil }
}

func changeManifest(t *testing.T, r *Record, change func(*Manifest)) {
	t.Helper()
	var m Manifest
	if err := json.Unmarshal(r.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	change(&m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r.Manifest = raw
	r.Assets[0].Size = int64(len(raw))
}

// replaceRaw keeps the release's manifest size consistent so JSON-shape tests
// cannot pass merely because an unrelated size check caught the edited bytes.
func replaceRaw(r *Record, raw string) {
	r.Manifest = []byte(raw)
	r.Assets[0].Size = int64(len(r.Manifest))
}

func TestValidateNextManifest(t *testing.T) {
	var cases = map[string]func(*Record){
		"wrong repository":        func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Repository = "yetone/magpie" }) },
		"wrong channel":           func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Channel = "preview" }) },
		"zero sequence":           func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Sequence = 0 }) },
		"unsupported platform":    func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Platform = "darwin-arm64" }) },
		"false signature claim":   func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Integrity = "signed" }) },
		"bad hash":                func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Assets[0].SHA256 = "bad" }) },
		"empty asset":             func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Assets[0].Size = 0 }) },
		"duplicate executable":    func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Assets[1] = m.Assets[0] }) },
		"zip is not executable":   func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Assets[0].Name = "package.zip" }) },
		"unresolved tag":          func(r *Record) { r.CommitSHA = "main" },
		"wrong resolved SHA":      func(r *Record) { r.CommitSHA = strings.Repeat("c", 40) },
		"wrong tag":               func(r *Record) { r.Tag = "v0.1.0" },
		"wrong asset host":        func(r *Record) { r.Assets[1].URL = "https://example.invalid/Magpie-Next.exe" },
		"wrong asset tag":         func(r *Record) { r.Assets[1].URL = downloadBase + "next-other/Magpie-Next.exe" },
		"wrong manifest host":     func(r *Record) { r.Assets[0].URL = "https://example.invalid/manifest.json" },
		"missing asset":           func(r *Record) { r.Assets = r.Assets[:2] },
		"mismatched asset size":   func(r *Record) { r.Assets[1].Size++ },
		"duplicate release asset": func(r *Record) { r.Assets = append(r.Assets, r.Assets[1]) },
		"missing manifest":        func(r *Record) { r.Manifest = nil },
		"oversized manifest":      func(r *Record) { r.Manifest = []byte(strings.Repeat(" ", 65537)) },
		"trailing JSON":           func(r *Record) { replaceRaw(r, string(r.Manifest)+`{}`) },
		"duplicate JSON key": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"schema":1`, `"schema":1,"schema":1`, 1))
		},
		"unknown JSON key": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"schema":1`, `"schema":1,"feed":"https://example.invalid"`, 1))
		},
		"case alias sequence": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"sequence":10`, `"sequence":9,"Sequence":10`, 1))
		},
		"uppercase schema": func(r *Record) { replaceRaw(r, strings.Replace(string(r.Manifest), `"schema":1`, `"Schema":1`, 1)) },
		"case alias asset size": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"size":4`, `"size":1,"SIZE":4`, 1))
		},
		"duplicate asset key": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"size":4`, `"size":4,"size":4`, 1))
		},
		"unknown asset key": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"size":4`, `"size":4,"extra":true`, 1))
		},
		"draft":                func(r *Record) { r.Draft = true },
		"prerelease in stable": func(r *Record) { r.Prerelease = true },
	}
	good := fixture(t, strings.Repeat("a", 40), 10, false)
	if _, err := Validate(good, "stable"); err != nil {
		t.Fatal(err)
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			r := fixture(t, strings.Repeat("a", 40), 10, false)
			breakIt(&r)
			if got, err := Validate(r, "stable"); err == nil || got != nil {
				t.Fatalf("accepted invalid metadata: %+v, %v", got, err)
			}
		})
	}
}

func TestNextSelectionListsEveryPageAndKeepsChannelsApart(t *testing.T) {
	t.Setenv("MAGPIE_UPDATE_FEED", "https://example.invalid/upstream")
	a := fixture(t, strings.Repeat("a", 40), 10, false)
	b := fixture(t, strings.Repeat("b", 40), 11, false)
	p := fixture(t, strings.Repeat("c", 40), 99, true)
	calls := 0
	page := func(_ context.Context, url string) ([]Record, bool, error) {
		calls++
		want := fmt.Sprintf("https://api.github.com/repos/xue-ding-e/Magpie-Next/releases?per_page=100&page=%d", calls)
		if url != want {
			t.Fatalf("unexpected source: %s", url)
		}
		if calls == 1 {
			return []Record{a, p}, true, nil
		}
		return []Record{b}, false, nil
	}
	got, err := Select(context.Background(), "stable", legacyState(), page)
	if err != nil || got == nil || got.Identity.Sequence != 11 || calls != 2 {
		t.Fatalf("wrong latest: %+v, %v, calls=%d", got, err, calls)
	}
	got, err = Select(context.Background(), "preview", legacyState(), onePage(a, b, p))
	if err != nil || got == nil || got.Identity.Sequence != 99 {
		t.Fatalf("prerelease was not found: %+v, %v", got, err)
	}
}

func TestNextHighWaterRejectsMutationAndDowngrade(t *testing.T) {
	a := fixture(t, strings.Repeat("a", 40), 10, false)
	old, err := Validate(a, "stable")
	if err != nil {
		t.Fatal(err)
	}
	s := legacyState()
	s.HighWater = &old.Identity
	s.Installed = &old.Identity
	s.InstalledSHA = old.Identity.SourceSHA
	s.InstalledVersion = old.Identity.Version
	low := fixture(t, strings.Repeat("b", 40), 9, false)
	if got, err := Select(context.Background(), "stable", s, onePage(low, a)); err != nil || got != nil {
		t.Fatalf("downgraded: %+v, %v", got, err)
	}
	changeManifest(t, &a, func(m *Manifest) { m.Assets[0].SHA256 = strings.Repeat("c", 64) })
	if got, err := Select(context.Background(), "stable", s, onePage(a)); err == nil || got != nil {
		t.Fatalf("accepted changed manifest: %+v, %v", got, err)
	}
	changedSequence := fixture(t, old.Identity.SourceSHA, 11, false)
	if _, err := Select(context.Background(), "stable", s, onePage(changedSequence)); err == nil {
		t.Fatal("same version was republished under another sequence")
	}
	reused := fixture(t, strings.Repeat("b", 40), 10, false)
	if _, err := Select(context.Background(), "stable", s, onePage(reused)); err == nil {
		t.Fatal("same sequence was reused by another version")
	}
}

func TestNextUnknownStateAndPartialScansFailClosed(t *testing.T) {
	a := fixture(t, strings.Repeat("a", 40), 10, false)
	s := legacyState()
	s.ReadOK = false
	if _, err := Select(context.Background(), "stable", s, onePage(a)); err == nil {
		t.Fatal("bad state read became empty")
	}
	s = legacyState()
	s.InstalledSHA = strings.Repeat("d", 40)
	s.InstalledVersion = tagFor(s.InstalledSHA)
	if _, err := Select(context.Background(), "stable", s, onePage(a)); err == nil {
		t.Fatal("unknown legacy build migrated")
	}
	calls := 0
	page := func(context.Context, string) ([]Record, bool, error) {
		calls++
		if calls == 1 {
			return []Record{a}, true, nil
		}
		return nil, false, errors.New("synthetic page failure")
	}
	if got, err := Select(context.Background(), "stable", legacyState(), page); err == nil || got != nil {
		t.Fatal("partial page result escaped")
	}
	page = func(context.Context, string) ([]Record, bool, error) { return nil, true, nil }
	if _, err := Select(context.Background(), "stable", legacyState(), page); err == nil {
		t.Fatal("page cap became completion")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Select(ctx, "stable", legacyState(), onePage(a)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNextLegacyZIPIsNeverAnInstallCandidate(t *testing.T) {
	s := legacyState()
	var old = Record{Tag: s.InstalledVersion, CommitSHA: s.InstalledSHA}
	got, err := Select(context.Background(), "stable", s, onePage(old))
	if err != nil || got != nil {
		t.Fatalf("legacy ZIP became candidate: %+v, %v", got, err)
	}
	old.CommitSHA = strings.Repeat("a", 40)
	old.Tag = tagFor(old.CommitSHA)
	if _, err := Select(context.Background(), "stable", s, onePage(old)); err == nil {
		t.Fatal("unknown release without manifest was ignored")
	}
}

func TestNextAcceptedDownloadCanBeRetriedBeforeInstallation(t *testing.T) {
	a := fixture(t, strings.Repeat("a", 40), 10, false)
	b := fixture(t, strings.Repeat("b", 40), 11, false)
	installed, err := Validate(a, "stable")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := Validate(b, "stable")
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			s := legacyState()
			if !legacy {
				s.Installed = &installed.Identity
				s.InstalledSHA = installed.Identity.SourceSHA
				s.InstalledVersion = installed.Identity.Version
			}
			s.HighWater = &accepted.Identity
			for attempt := 0; attempt < 2; attempt++ {
				got, err := Select(context.Background(), "stable", s, onePage(a, b))
				if err != nil || got == nil || got.Identity != accepted.Identity {
					t.Fatalf("accepted uninstalled download cannot retry: %+v, %v", got, err)
				}
			}
			changed := fixture(t, strings.Repeat("b", 40), 11, false)
			changeManifest(t, &changed, func(m *Manifest) { m.Assets[0].SHA256 = strings.Repeat("c", 64) })
			if _, err := Select(context.Background(), "stable", s, onePage(changed)); err == nil {
				t.Fatal("retry accepted a changed digest")
			}
			if got, err := Select(context.Background(), "stable", s, onePage(a)); err != nil || got != nil {
				t.Fatalf("accepted high-water fell back to older candidate: %+v, %v", got, err)
			}
			s.Installed = &accepted.Identity
			s.InstalledSHA = accepted.Identity.SourceSHA
			s.InstalledVersion = accepted.Identity.Version
			if got, err := Select(context.Background(), "stable", s, onePage(a, b)); err != nil || got != nil {
				t.Fatalf("installed candidate retried: %+v, %v", got, err)
			}
		})
	}
}

func TestNextFinalPageCancellationFailsClosed(t *testing.T) {
	a := fixture(t, strings.Repeat("a", 40), 10, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	page := func(context.Context, string) ([]Record, bool, error) {
		cancel()
		return []Record{a}, false, nil
	}
	got, err := Select(ctx, "stable", legacyState(), page)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("final-page cancellation returned success: %+v, %v", got, err)
	}
}
