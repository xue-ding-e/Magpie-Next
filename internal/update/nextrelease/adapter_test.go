package nextrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type adapterReply struct {
	body    []byte
	status  int
	headers map[string]string
	chunked bool
	hold    func(*http.Request)
}

type adapterFixtureData struct {
	record   Record
	observed ReleaseMetadata
	routes   map[string]adapterReply
	refPath  string
	commit   string
	manifest string
	tagPaths []string
}

func adapterJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func adapterDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func adapterTarget(kind, sha string) map[string]any {
	return map[string]any{"type": kind, "sha": sha, "url": githubAPIBase + "/git/" + kind + "s/" + sha}
}

func newAdapterFixture(t *testing.T, depth int, preview bool) adapterFixtureData {
	t.Helper()
	var f adapterFixtureData
	f.record = fixture(t, strings.Repeat("a", 40), 10, preview)
	f.routes = make(map[string]adapterReply)
	f.refPath = "/git/ref/tags/" + f.record.Tag
	f.commit = "/git/commits/" + f.record.CommitSHA
	f.manifest = "/releases/assets/70"
	var assets = make([]map[string]any, 0, len(f.record.Assets))
	var manifest Manifest
	if err := json.Unmarshal(f.record.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	for i, asset := range f.record.Assets {
		digest := adapterDigest(f.record.Manifest)
		if i > 0 {
			digest = "sha256:" + manifest.Assets[i-1].SHA256
		}
		assets = append(assets, map[string]any{"id": 70 + i, "url": githubAPIBase + "/releases/assets/" + strconv.Itoa(70+i),
			"name": asset.Name, "size": asset.Size, "state": "uploaded", "digest": digest, "browser_download_url": asset.URL})
	}
	f.routes["/releases/7"] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{
		"id": 7, "url": githubAPIBase + "/releases/7", "tag_name": f.record.Tag,
		"target_commitish": strings.Repeat("b", 40), "draft": false, "prerelease": preview, "assets": assets})}
	var target = adapterTarget("commit", f.record.CommitSHA)
	for i := depth - 1; i >= 0; i-- {
		sha := fmt.Sprintf("%040x", i+1)
		path := "/git/tags/" + sha
		name := "nested-tag"
		if i == 0 {
			name = f.record.Tag
		}
		f.routes[path] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{
			"sha": sha, "url": githubAPIBase + path, "tag": name, "object": target})}
		f.tagPaths = append([]string{path}, f.tagPaths...)
		target = adapterTarget("tag", sha)
	}
	f.routes[f.refPath] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{
		"ref": "refs/tags/" + f.record.Tag, "url": githubAPIBase + "/git/refs/tags/" + f.record.Tag, "object": target})}
	f.routes[f.commit] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{
		"sha": f.record.CommitSHA, "url": githubAPIBase + f.commit, "tree": map[string]any{"sha": strings.Repeat("d", 40)}})}
	f.routes[f.manifest] = adapterReply{status: 200, body: f.record.Manifest}
	f.refreshObservation(t)
	return f
}

func (f *adapterFixtureData) refreshObservation(t *testing.T) {
	t.Helper()
	list, err := decodeReleasePage(append(append([]byte{'['}, f.routes["/releases/7"].body...), ']'))
	if err != nil || len(list) != 1 {
		t.Fatalf("invalid synthetic observation: %v", err)
	}
	f.observed = list[0]
}

func editAdapterJSON(t *testing.T, reply *adapterReply, change func(map[string]any)) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(reply.body, &value); err != nil {
		t.Fatal(err)
	}
	change(value)
	reply.body = adapterJSON(t, value)
}

// The fixture maps only explicitly allowed own-repository requests onto loopback.
// Unexpected requests, including executable downloads or broken redirect URLs,
// are recorded and rejected before DNS; Offline alone cannot guard this client.
func adapterServer(t *testing.T, f adapterFixtureData, change func(string, int, *adapterReply)) (*http.Client, *metadataRequests) {
	t.Helper()
	var mu sync.Mutex
	var visits = make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/repos/"+repository)
		mu.Lock()
		visits[path]++
		visit := visits[path]
		mu.Unlock()
		reply := f.routes[path]
		if change != nil {
			change(path, visit, &reply)
		}
		for key, value := range reply.headers {
			w.Header().Set(key, value)
		}
		w.Header().Set("Set-Cookie", "synthetic-cookie=must-not-be-returned")
		w.WriteHeader(reply.status)
		if reply.hold != nil {
			w.(http.Flusher).Flush()
			reply.hold(req)
			return
		}
		if reply.chunked {
			w.(http.Flusher).Flush()
		}
		w.Write(reply.body)
	}))
	t.Cleanup(server.Close)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newMetadataClient()
	transport := client.Transport.(*http.Transport)
	t.Cleanup(transport.CloseIdleConnections)
	var requests metadataRequests
	client.Transport = metadataRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests.mu.Lock()
		requests.urls = append(requests.urls, req.URL.String())
		requests.mu.Unlock()
		path := strings.TrimPrefix(req.URL.Path, "/repos/"+repository)
		allowed := req.URL.String() == githubAPIBase+path || (path == "/redirected-manifest" && req.URL.String() == "https://release-assets.githubusercontent.com/redirected-manifest")
		if _, ok := f.routes[path]; !ok || !allowed {
			return nil, errors.New("test rejected an unlisted or foreign adapter request before DNS")
		}
		accept := "application/vnd.github+json"
		if path == f.manifest || path == "/redirected-manifest" {
			accept = "application/octet-stream"
		}
		if req.Method != http.MethodGet || req.Header.Get("Accept") != accept || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("Proxy-Authorization") != "" {
			t.Error("adapter transmitted credentials or used the wrong HTTP method/media type")
		}
		copy := req.Clone(req.Context())
		copy.URL.Scheme, copy.URL.Host = local.Scheme, local.Host
		copy.Host = "api.github.com"
		return transport.RoundTrip(copy)
	})
	return client, &requests
}

func requireNoRecord(t *testing.T, got Record, err error, contains string) {
	t.Helper()
	if !reflect.DeepEqual(got, Record{}) || err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("wanted fail-closed record (%q), got tag=%q manifest=%d err=%v", contains, got.Tag, len(got.Manifest), err)
	}
}

func TestAdapterResolvesLightweightAndAnnotatedTagsWithoutBinaryDownloads(t *testing.T) {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "MAGPIE_UPDATE_FEED", "HTTPS_PROXY"} {
		t.Setenv(key, "synthetic-must-not-be-used")
	}
	for _, depth := range []int{0, 1, maxTagDepth} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("depth=%d/preview=%v", depth, preview), func(t *testing.T) {
				f := newAdapterFixture(t, depth, preview)
				client, requests := adapterServer(t, f, nil)
				channel := "stable"
				if preview {
					channel = "preview"
				}
				got, err := readReleaseRecord(context.Background(), client, f.observed, channel)
				if err != nil || !reflect.DeepEqual(got, f.record) || requests.count() != 7+2*depth {
					t.Fatalf("wrong materialized record: tag=%q sha=%q requests=%d err=%v", got.Tag, got.CommitSHA, requests.count(), err)
				}
				if _, err := Validate(got, channel); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestAdapterFollowsGitHubAssetRedirectWithOptionalAPIDigests(t *testing.T) {
	f := newAdapterFixture(t, 0, false)
	release := f.routes["/releases/7"]
	editAdapterJSON(t, &release, func(m map[string]any) {
		for _, raw := range m["assets"].([]any) {
			delete(raw.(map[string]any), "digest")
		}
	})
	f.routes["/releases/7"] = release
	f.routes["/redirected-manifest"] = f.routes[f.manifest]
	f.routes[f.manifest] = adapterReply{status: http.StatusFound, headers: map[string]string{"Location": "https://release-assets.githubusercontent.com/redirected-manifest"}}
	f.refreshObservation(t)
	client, requests := adapterServer(t, f, nil)
	got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
	if err != nil || string(got.Manifest) != string(f.record.Manifest) || requests.count() != 8 {
		t.Fatalf("GitHub's normal asset redirect failed: %v, requests=%d", err, requests.count())
	}
}

func TestAssetRedirectRejectsForeignOrUnsafeHosts(t *testing.T) {
	for _, destination := range []string{"http://release-assets.githubusercontent.com/asset", "https://release-assets.githubusercontent.com.evil.invalid/asset", "https://user@release-assets.githubusercontent.com/asset", "https://release-assets.githubusercontent.com:444/asset", "https://github.com/yetone/magpie/releases/asset", "https://127.0.0.1/asset"} {
		req, err := http.NewRequest(http.MethodGet, destination, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := AssetRedirect(req, []*http.Request{{}}); err == nil {
			t.Fatal("unsafe asset redirect accepted", destination)
		}
	}
}

func TestAdapterRejectsStaleObservationAndWrongChannelBeforeFetchingBytes(t *testing.T) {
	var cases = map[string]func(*ReleaseMetadata){
		"tag":                      func(r *ReleaseMetadata) { r.Tag = "next-bbbbbbbbbbbb" },
		"asset identity":           func(r *ReleaseMetadata) { r.Assets[0].ID++ },
		"duplicate observed asset": func(r *ReleaseMetadata) { r.Assets[1] = r.Assets[0] },
		"draft":                    func(r *ReleaseMetadata) { r.Draft = true },
		"channel":                  func(r *ReleaseMetadata) { r.Prerelease = true },
		"path traversal":           func(r *ReleaseMetadata) { r.Tag = "../other" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAdapterFixture(t, 0, false)
			change(&f.observed)
			client, requests := adapterServer(t, f, nil)
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "")
			if requests.count() > 1 {
				t.Fatal("stale/invalid observation fetched bytes or Git objects")
			}
		})
	}
}

func TestAdapterRejectsAmbiguousOrForeignGitAndReleaseIdentity(t *testing.T) {
	var cases = []struct {
		name  string
		stage string
		edit  func(*testing.T, *adapterReply)
	}{
		{"foreign release", "release", func(t *testing.T, r *adapterReply) {
			r.body = []byte(strings.Replace(string(r.body), githubAPIBase+"/releases/7", "https://api.github.com/repos/yetone/magpie/releases/7", 1))
		}},
		{"duplicate release key", "release", func(t *testing.T, r *adapterReply) {
			r.body = []byte(strings.Replace(string(r.body), `"id":7`, `"id":7,"id":7`, 1))
		}},
		{"ref prefix not exact", "ref", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["ref"] = m["ref"].(string) + "-other" })
		}},
		{"foreign ref", "ref", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["url"] = "https://example.invalid/ref" })
		}},
		{"ambiguous ref array", "ref", func(t *testing.T, r *adapterReply) { r.body = append(append([]byte{'['}, r.body...), ']') }},
		{"duplicate ref object", "ref", func(t *testing.T, r *adapterReply) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(r.body, &fields); err != nil {
				t.Fatal(err)
			}
			r.body = []byte(strings.Replace(string(r.body), `"object":`, `"object":`+string(fields["object"])+`,"object":`, 1))
		}},
		{"foreign target", "ref", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) {
				m["object"].(map[string]any)["url"] = "https://api.github.com/repos/yetone/magpie/git/tags/1"
			})
		}},
		{"non commit target", "ref", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("tree", strings.Repeat("a", 40)) })
		}},
		{"uppercase target SHA", "ref", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("commit", strings.Repeat("A", 40)) })
		}},
		{"tag name mismatch", "tag", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["tag"] = "other" })
		}},
		{"tag SHA mismatch", "tag", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["sha"] = strings.Repeat("b", 40) })
		}},
		{"duplicate tag SHA", "tag", func(t *testing.T, r *adapterReply) {
			r.body = []byte(strings.Replace(string(r.body), `"sha":"0000000000000000000000000000000000000001"`, `"sha":"0000000000000000000000000000000000000001","sha":"0000000000000000000000000000000000000001"`, 1))
		}},
		{"foreign tag object", "tag", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["url"] = "https://example.invalid/tag" })
		}},
		{"commit SHA mismatch", "commit", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["sha"] = strings.Repeat("b", 40) })
		}},
		{"foreign commit object", "commit", func(t *testing.T, r *adapterReply) {
			editAdapterJSON(t, r, func(m map[string]any) { m["url"] = "https://example.invalid/commit" })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdapterFixture(t, 1, false)
			path := map[string]string{"release": "/releases/7", "ref": f.refPath, "tag": f.tagPaths[0], "commit": f.commit}[tc.stage]
			client, _ := adapterServer(t, f, func(p string, _ int, r *adapterReply) {
				if p == path {
					tc.edit(t, r)
				}
			})
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "")
		})
	}
}

func TestAdapterRejectsTagCyclesDepthAndTagPrefixMismatch(t *testing.T) {
	for _, mode := range []string{"cycle", "depth", "prefix mismatch"} {
		t.Run(mode, func(t *testing.T) {
			depth := 1
			if mode == "depth" {
				depth = maxTagDepth + 1
			}
			f := newAdapterFixture(t, depth, false)
			if mode == "prefix mismatch" {
				sha := strings.Repeat("b", 40)
				f.routes["/git/commits/"+sha] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{"sha": sha, "url": githubAPIBase + "/git/commits/" + sha})}
			}
			client, requests := adapterServer(t, f, func(p string, _ int, r *adapterReply) {
				if p != f.tagPaths[0] {
					return
				}
				if mode == "cycle" {
					editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("tag", strings.Repeat("0", 39)+"1") })
				}
				if mode == "prefix mismatch" {
					editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("commit", strings.Repeat("b", 40)) })
				}
			})
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "")
			if requests.count() > maxRecordRequests {
				t.Fatal("tag traversal was unbounded")
			}
			if mode == "depth" && requests.count() != 2+maxTagDepth {
				t.Fatalf("depth cap fetched too many Git objects: %d", requests.count())
			}
		})
	}
}

func TestAdapterRequiresOriginalManifestContractDespiteMatchingAPIDigest(t *testing.T) {
	var cases = map[string]func(*Record){
		"wrong manifest source":     func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.SourceSHA = strings.Repeat("b", 40) }) },
		"wrong manifest repository": func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Repository = "yetone/magpie" }) },
		"false signature claim":     func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Integrity = "signed" }) },
		"wrong manifest channel":    func(r *Record) { changeManifest(t, r, func(m *Manifest) { m.Channel = "preview" }) },
		"duplicate manifest key": func(r *Record) {
			replaceRaw(r, strings.Replace(string(r.Manifest), `"sequence":10`, `"sequence":10,"sequence":10`, 1))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAdapterFixture(t, 0, false)
			change(&f.record)
			manifest := f.routes[f.manifest]
			manifest.body = f.record.Manifest
			f.routes[f.manifest] = manifest
			release := f.routes["/releases/7"]
			editAdapterJSON(t, &release, func(m map[string]any) {
				asset := m["assets"].([]any)[0].(map[string]any)
				asset["size"] = len(f.record.Manifest)
				asset["digest"] = adapterDigest(f.record.Manifest)
			})
			f.routes["/releases/7"] = release
			f.refreshObservation(t)
			client, _ := adapterServer(t, f, nil)
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "manifest")
		})
	}
}

func TestAdapterManifestAndExecutableDigestBindings(t *testing.T) {
	for _, mode := range []string{"manifest digest", "executable digest", "duplicate digest", "size underflow", "binary metadata instead of manifest", "legacy ZIP"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdapterFixture(t, 0, false)
			if mode == "legacy ZIP" {
				r := f.routes["/releases/7"]
				r.body = []byte(strings.ReplaceAll(strings.ReplaceAll(string(r.body), f.record.Tag, "next-d65e4026f085"), "manifest.json", "portable.zip"))
				f.routes["/releases/7"] = r
				f.refreshObservation(t)
			}
			client, _ := adapterServer(t, f, func(p string, _ int, r *adapterReply) {
				if p == f.manifest {
					switch mode {
					case "manifest digest":
						r.body = []byte(strings.Replace(string(r.body), `"sequence":10`, `"sequence":11`, 1))
					case "size underflow":
						r.body = r.body[:len(r.body)-1]
					case "binary metadata instead of manifest":
						r.body = []byte(`{"id":70,"name":"manifest.json"}`)
					}
				}
				if p == "/releases/7" {
					switch mode {
					case "executable digest":
						editAdapterJSON(t, r, func(m map[string]any) {
							m["assets"].([]any)[1].(map[string]any)["digest"] = "sha256:" + strings.Repeat("c", 64)
						})
					case "duplicate digest":
						r.body = []byte(strings.Replace(string(r.body), `"digest":`, `"digest":"sha256:`+strings.Repeat("0", 64)+`","digest":`, 1))
					}
				}
			})
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "")
			if mode == "legacy ZIP" && !strings.Contains(err.Error(), "manual update") {
				t.Fatal("legacy failure is not diagnosable", err)
			}
		})
	}
}

func TestAdapterRechecksReleaseTagAndAnnotatedChain(t *testing.T) {
	for _, mode := range []string{"asset replacement", "asset digest", "ref moved", "annotated target moved", "annotated chain changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdapterFixture(t, 1, false)
			other := strings.Repeat("b", 40)
			f.routes["/git/commits/"+other] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{"sha": other, "url": githubAPIBase + "/git/commits/" + other})}
			f.routes["/git/tags/"+other] = adapterReply{status: 200, body: adapterJSON(t, map[string]any{"sha": other, "url": githubAPIBase + "/git/tags/" + other, "tag": "nested-tag", "object": adapterTarget("commit", f.record.CommitSHA)})}
			client, _ := adapterServer(t, f, func(p string, visit int, r *adapterReply) {
				if visit != 2 {
					return
				}
				if p == "/releases/7" && mode == "asset replacement" {
					editAdapterJSON(t, r, func(m map[string]any) {
						a := m["assets"].([]any)[0].(map[string]any)
						a["id"] = 99
						a["url"] = githubAPIBase + "/releases/assets/99"
					})
				}
				if p == "/releases/7" && mode == "asset digest" {
					editAdapterJSON(t, r, func(m map[string]any) {
						m["assets"].([]any)[0].(map[string]any)["digest"] = "sha256:" + strings.Repeat("c", 64)
					})
				}
				if p == f.refPath && mode == "ref moved" {
					editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("commit", other) })
				}
				if p == f.tagPaths[0] && mode == "annotated target moved" {
					editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("commit", other) })
				}
				if p == f.tagPaths[0] && mode == "annotated chain changed" {
					editAdapterJSON(t, r, func(m map[string]any) { m["object"] = adapterTarget("tag", other) })
				}
			})
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "materialization")
		})
	}
}

func TestAdapterRejectsRedirectStatusAndBodyFailures(t *testing.T) {
	for _, mode := range []string{"redirect", "404", "content length", "chunked overflow", "truncated read", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdapterFixture(t, 0, false)
			client, requests := adapterServer(t, f, func(p string, _ int, r *adapterReply) {
				if p != f.manifest {
					return
				}
				r.headers = make(map[string]string)
				switch mode {
				case "redirect":
					r.status = 302
					r.headers["Location"] = "https://example.invalid/release-assets/manifest.json"
				case "404":
					r.status = 404
				case "content length":
					r.headers["Content-Length"] = strconv.Itoa(maxManifestBytes + 1)
				case "chunked overflow":
					r.body = append(append([]byte{}, r.body...), ' ')
					r.chunked = true
				case "truncated read":
					r.headers["Content-Length"] = strconv.Itoa(len(r.body))
					r.body = r.body[:len(r.body)-1]
				case "encoding":
					r.headers["Content-Encoding"] = "gzip"
				}
			})
			got, err := readReleaseRecord(context.Background(), client, f.observed, "stable")
			requireNoRecord(t, got, err, "")
			if requests.count() != 4 {
				t.Fatalf("failure made another request: %d", requests.count())
			}
		})
	}
}

func TestAdapterContextCancellationAndBudgets(t *testing.T) {
	t.Run("cancelled before HTTP", func(t *testing.T) {
		f := newAdapterFixture(t, 0, false)
		client, requests := adapterServer(t, f, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := readReleaseRecord(ctx, client, f.observed, "stable")
		requireNoRecord(t, got, err, "")
		if !errors.Is(err, context.Canceled) || requests.count() != 0 {
			t.Fatal("pre-cancelled operation reached HTTP", err)
		}
	})
	t.Run("cancelled during held manifest body", func(t *testing.T) {
		f := newAdapterFixture(t, 0, false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered := make(chan struct{})
		client, _ := adapterServer(t, f, func(p string, _ int, r *adapterReply) {
			if p == f.manifest {
				r.hold = func(req *http.Request) {
					select {
					case <-entered:
					case <-req.Context().Done():
						return
					}
					cancel()
					<-req.Context().Done()
				}
			}
		})
		transport := client.Transport
		client.Transport = metadataRoundTrip(func(req *http.Request) (*http.Response, error) {
			resp, err := transport.RoundTrip(req)
			if err == nil && strings.HasSuffix(req.URL.Path, f.manifest) {
				resp.Body = &signalAdapterRead{ReadCloser: resp.Body, entered: entered}
			}
			return resp, err
		})
		got, err := readReleaseRecord(ctx, client, f.observed, "stable")
		requireNoRecord(t, got, err, "")
		if !errors.Is(err, context.Canceled) {
			t.Fatal("held manifest read did not return cancellation", err)
		}
	})
	t.Run("cancelled after final body", func(t *testing.T) {
		f := newAdapterFixture(t, 0, false)
		client, _ := adapterServer(t, f, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		transport := client.Transport
		calls := 0
		client.Transport = metadataRoundTrip(func(req *http.Request) (*http.Response, error) {
			resp, err := transport.RoundTrip(req)
			if strings.HasSuffix(req.URL.Path, f.commit) {
				calls++
				if calls == 2 && err == nil {
					resp.Body = &cancelAtEOF{ReadCloser: resp.Body, cancel: cancel}
				}
			}
			return resp, err
		})
		got, err := readReleaseRecord(ctx, client, f.observed, "stable")
		requireNoRecord(t, got, err, "")
		if !errors.Is(err, context.Canceled) {
			t.Fatal("last body cancellation became success", err)
		}
	})
	for _, mode := range []string{"requests", "total bytes"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdapterFixture(t, 0, false)
			client, requests := adapterServer(t, f, nil)
			var reader = recordReader{client: client, remaining: len(f.routes["/releases/7"].body)}
			if mode == "requests" {
				reader.requests = maxRecordRequests
			} else {
				if _, err := reader.get(context.Background(), "/releases/7", "application/vnd.github+json", maxReleasePage); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reader.get(context.Background(), "/releases/7", "application/vnd.github+json", maxReleasePage); err == nil {
				t.Fatal("budget became success")
			}
			want := 0
			if mode == "total bytes" {
				want = 1
			}
			if requests.count() != want {
				t.Fatal("request escaped budget", requests.count())
			}
		})
	}
}

type signalAdapterRead struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (r *signalAdapterRead) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	return r.ReadCloser.Read(p)
}
