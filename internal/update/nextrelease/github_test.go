package nextrelease

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type metadataRoundTrip func(*http.Request) (*http.Response, error)

func (f metadataRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type metadataRequests struct {
	mu   sync.Mutex
	urls []string
}

func (r *metadataRequests) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.urls)
}

// Every request, including a request made by broken redirect/feed code, is
// rewritten to this server or rejected before any DNS/network access.
func metadataServer(t *testing.T, handler http.HandlerFunc) (*http.Client, *metadataRequests) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newMetadataClient()
	transport := client.Transport.(*http.Transport)
	var requests metadataRequests
	client.Transport = metadataRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests.mu.Lock()
		requests.urls = append(requests.urls, req.URL.String())
		requests.mu.Unlock()
		query := req.URL.Query()
		page, err := strconv.Atoi(query.Get("page"))
		if err != nil || page < 1 || req.URL.String() != fmt.Sprintf("%s/releases?per_page=100&page=%d", githubAPIBase, page) {
			return nil, errors.New("test refused a noncanonical or foreign metadata URL")
		}
		copy := req.Clone(req.Context())
		copy.URL.Scheme, copy.URL.Host = local.Scheme, local.Host
		copy.Host = "api.github.com"
		return transport.RoundTrip(copy)
	})
	t.Cleanup(transport.CloseIdleConnections)
	return client, &requests
}

// Shape follows GitHub's release-list response; identity and asset bytes are
// synthetic. target_commitish is deliberately a branch, never a resolved SHA.
func metadataFixture(id int) string {
	tag := fmt.Sprintf("next-%012x", id)
	return fmt.Sprintf(`{"id":%d,"url":%q,"tag_name":%q,"target_commitish":"main","draft":false,"prerelease":false,"author":{"login":"synthetic"},"assets":[{"id":%d,"name":"manifest.json","state":"uploaded","size":123,"url":%q,"browser_download_url":%q}]}`,
		id, githubAPIBase+"/releases/"+strconv.Itoa(id), tag, id*10,
		githubAPIBase+"/releases/assets/"+strconv.Itoa(id*10), downloadBase+tag+"/manifest.json")
}

func TestGitHubMetadataScansFullShortAndEmptyPages(t *testing.T) {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "MAGPIE_UPDATE_FEED", "HTTPS_PROXY"} {
		t.Setenv(key, "synthetic-value-that-must-not-be-sent")
	}
	client, requests := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("metadata request transmitted credentials or used a non-read method")
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") != "Magpie-Next-release-metadata" {
			t.Error("missing GitHub protocol headers")
		}
		switch r.URL.Query().Get("page") {
		case "1":
			var entries = make([]string, 100)
			for i := range entries {
				entries[i] = metadataFixture(i + 1)
			}
			// Even an attacker-controlled Link never becomes a request URL.
			w.Header().Set("Link", `<https://example.invalid/stolen>; rel="next"`)
			fmt.Fprint(w, "["+strings.Join(entries, ",")+"]")
		case "2":
			fmt.Fprint(w, "["+strings.Replace(metadataFixture(101), `"prerelease":false`, `"prerelease":true`, 1)+"]")
		case "3":
			fmt.Fprint(w, "["+strings.Replace(metadataFixture(102), `"draft":false`, `"draft":true`, 1)+"]")
		case "4":
			w.Header().Set("Link", `<https://api.github.com/repositories/123/releases?page=3>; rel="prev"`)
			fmt.Fprint(w, "[]")
		default:
			t.Error("request beyond the terminal empty page")
		}
	})
	got, err := listReleaseMetadata(context.Background(), client)
	if err != nil || len(got) != 102 || requests.count() != 4 {
		t.Fatalf("full/short page became terminal: len=%d requests=%d err=%v", len(got), requests.count(), err)
	}
	if !got[100].Prerelease || !got[101].Draft || got[0].Assets[0].Size != 123 || got[0].Tag != "next-000000000001" {
		t.Fatalf("metadata was changed: %+v", got[0])
	}
}

func TestGitHubMetadataClientHasNoAmbientAuthentication(t *testing.T) {
	client := newMetadataClient()
	defer client.CloseIdleConnections()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == http.DefaultTransport || transport.Proxy != nil || transport.TLSClientConfig != nil || client.Jar != nil {
		t.Fatal("client inherited a transport, proxy, cookie jar or TLS client credentials")
	}
	if client.Timeout <= 0 || client.Timeout > releaseReadLimit || transport.ResponseHeaderTimeout <= 0 || transport.MaxResponseHeaderBytes > 64*1024 || !transport.DisableCompression {
		t.Fatal("client has unbounded reads or implicit decompression")
	}
	if client.CheckRedirect == nil || client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("client permits redirects")
	}
}

func TestGitHubMetadataRejectsStatusAndRedirectsWithoutFollowing(t *testing.T) {
	for _, status := range []int{201, 204, 301, 302, 303, 307, 308, 401, 403, 404, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			client, requests := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "2" {
					fmt.Fprint(w, "[]")
					return
				}
				w.Header().Set("Location", githubAPIBase+"/releases?per_page=100&page=2")
				w.Header().Set("Set-Cookie", "session=synthetic")
				w.WriteHeader(status)
				fmt.Fprint(w, "[]")
			})
			got, err := listReleaseMetadata(context.Background(), client)
			if err == nil || got != nil || requests.count() != 1 {
				t.Fatalf("status accepted or redirected: got=%v err=%v requests=%d", got, err, requests.count())
			}
		})
	}
}

func TestGitHubMetadataRejectsMalformedOrForeignMetadata(t *testing.T) {
	good := "[" + metadataFixture(1) + "]"
	var cases = map[string]string{
		"null list":         "null",
		"object list":       "{}",
		"trailing document": good + "[]",
		"truncated JSON":    good[:len(good)-1],
		"null release":      "[null]",
		"wrong repository":  strings.Replace(good, githubAPIBase, "https://api.github.com/repos/yetone/magpie", 1),
		"duplicate ID key":  strings.Replace(good, `"id":1,`, `"id":1,"id":1,`, 1),
		"missing draft":     strings.Replace(good, `"draft":false,`, "", 1),
		"null draft":        strings.Replace(good, `"draft":false`, `"draft":null`, 1),
		"uppercase draft":   strings.Replace(good, `"draft":false`, `"Draft":false`, 1),
		"string boolean":    strings.Replace(good, `"draft":false`, `"draft":"false"`, 1),
		"zero ID":           strings.Replace(good, `"id":1,`, `"id":0,`, 1),
		"overflow ID":       strings.Replace(good, `"id":1,`, `"id":9223372036854775808,`, 1),
		"foreign asset API": strings.Replace(good, githubAPIBase+"/releases/assets/10", "https://api.github.com/repos/yetone/magpie/releases/assets/10", 1),
		"foreign download":  strings.Replace(good, downloadBase, "https://example.invalid/", 1),
		"credential URL":    strings.Replace(good, downloadBase, "https://user:secret@github.com/"+repository+"/releases/download/", 1),
		"asset traversal":   strings.Replace(good, `"name":"manifest.json"`, `"name":".."`, 1),
		"null assets":       strings.Replace(good, `"assets":[`, `"assets":null,"unused":[`, 1),
		"unfinished upload": strings.Replace(good, `"state":"uploaded"`, `"state":"starter"`, 1),
		"negative size":     strings.Replace(good, `"size":123`, `"size":-1`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "1" {
					fmt.Fprint(w, body)
					return
				}
				fmt.Fprint(w, "[]")
			})
			if got, err := listReleaseMetadata(context.Background(), client); err == nil || got != nil {
				t.Fatalf("accepted invalid metadata: %v, %v", got, err)
			}
		})
	}
}

func TestGitHubMetadataRejectsDuplicateOrRepeatingPages(t *testing.T) {
	for _, mode := range []string{"same page", "repeated page", "same tag", "same ID"} {
		t.Run(mode, func(t *testing.T) {
			client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page > 2 || (page == 2 && mode != "repeated page") {
					fmt.Fprint(w, "[]")
					return
				}
				one, two := metadataFixture(1), metadataFixture(2)
				switch mode {
				case "same page":
					fmt.Fprint(w, "["+one+","+one+"]")
				case "repeated page":
					fmt.Fprint(w, "["+one+"]")
				case "same tag":
					two = strings.ReplaceAll(two, "next-000000000002", "next-000000000001")
					fmt.Fprint(w, "["+one+","+two+"]")
				case "same ID":
					two = strings.Replace(two, `"id":2`, `"id":1`, 1)
					two = strings.Replace(two, "/releases/2\"", "/releases/1\"", 1)
					fmt.Fprint(w, "["+one+","+two+"]")
				}
			})
			if got, err := listReleaseMetadata(context.Background(), client); err == nil || got != nil {
				t.Fatalf("duplicate observation accepted: %v, %v", got, err)
			}
		})
	}
}

func TestGitHubMetadataFailsClosedOnLaterFailureAndPageCap(t *testing.T) {
	for _, mode := range []string{"later status", "empty with next", "malformed links", "page cap"} {
		t.Run(mode, func(t *testing.T) {
			client, requests := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page == 1 || mode == "page cap" {
					fmt.Fprint(w, "["+metadataFixture(page)+"]")
					return
				}
				switch mode {
				case "later status":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "empty with next":
					w.Header().Set("Link", `<https://api.github.com/repositories/123/releases?page=3>; rel="next"`)
				case "malformed links":
					w.Header().Set("Link", "not a pagination link")
				}
				fmt.Fprint(w, "[]")
			})
			got, err := listReleaseMetadata(context.Background(), client)
			if err == nil || got != nil || requests.count() > maxReleasePages {
				t.Fatalf("incomplete scan escaped: %v, %v, requests=%d", got, err, requests.count())
			}
			if mode == "page cap" && requests.count() != maxReleasePages {
				t.Fatalf("page cap failed for the wrong reason: %v, requests=%d", err, requests.count())
			}
		})
	}
}

func TestGitHubMetadataBoundsBodiesAndTotalBytes(t *testing.T) {
	for _, mode := range []string{"length", "chunked", "truncated", "encoding", "total bytes", "too many records"} {
		t.Run(mode, func(t *testing.T) {
			client, requests := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "length":
					w.Header().Set("Content-Length", strconv.Itoa(maxReleasePage+1))
					fmt.Fprint(w, "[]")
				case "chunked":
					w.(http.Flusher).Flush()
					fmt.Fprint(w, "[]"+strings.Repeat(" ", maxReleasePage-1))
				case "truncated":
					w.Header().Set("Content-Length", "1000")
					fmt.Fprint(w, "[]")
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
					fmt.Fprint(w, "[]")
				case "total bytes":
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					body := "[" + metadataFixture(page) + "]"
					fmt.Fprint(w, body+strings.Repeat(" ", maxReleasePage-len(body)))
				case "too many records":
					var entries = make([]string, 101)
					for i := range entries {
						entries[i] = metadataFixture(i + 1)
					}
					fmt.Fprint(w, "["+strings.Join(entries, ",")+"]")
				}
			})
			got, err := listReleaseMetadata(context.Background(), client)
			if err == nil || got != nil {
				t.Fatalf("unsafe body accepted: %v, %v", got, err)
			}
			if mode == "total bytes" && requests.count() != maxReleaseScan/maxReleasePage {
				t.Fatalf("total cap failed for wrong reason: requests=%d, %v", requests.count(), err)
			}
		})
	}
}

type cancelAtEOF struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelAtEOF) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func TestGitHubMetadataCancellationBeforeDuringAndAfterBody(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		client, requests := metadataServer(t, func(http.ResponseWriter, *http.Request) { t.Error("cancelled request sent") })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got, err := listReleaseMetadata(ctx, client); got != nil || !errors.Is(err, context.Canceled) || requests.count() != 0 {
			t.Fatalf("pre-cancellation lost: %v, %v", got, err)
		}
	})
	t.Run("body read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			cancel()
			<-r.Context().Done()
		})
		if got, err := listReleaseMetadata(ctx, client); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("body cancellation lost: %v, %v", got, err)
		}
	})
	t.Run("final EOF", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "[]") })
		transport := client.Transport
		client.Transport = metadataRoundTrip(func(req *http.Request) (*http.Response, error) {
			resp, err := transport.RoundTrip(req)
			if err == nil {
				resp.Body = &cancelAtEOF{ReadCloser: resp.Body, cancel: cancel}
			}
			return resp, err
		})
		if got, err := listReleaseMetadata(ctx, client); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("final body cancellation became a successful empty listing: %v, %v", got, err)
		}
	})
}

func TestGitHubMetadataClientDeadlineFailure(t *testing.T) {
	client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	// No elapsed-time assertion. If reached, the handler withholds its body until
	// HTTP cancels it; a loaded runner can instead expire before reaching it.
	// The separate body-read cancellation case proves that path was entered.
	client.Timeout = 25 * time.Millisecond
	got, err := listReleaseMetadata(context.Background(), client)
	if got != nil || err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request deadline did not fail closed: %v, %v", got, err)
	}
	client.CloseIdleConnections()
}

func TestGitHubMetadataListingIsNotAHistoricalSnapshot(t *testing.T) {
	// If an unseen release disappears or moves to an already-read page during
	// pagination, no duplicate exposes it. This test documents the API boundary:
	// the result is observations only, never a proof that release 2 did not exist.
	client, _ := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, "["+metadataFixture(1)+"]")
		case "2":
			fmt.Fprint(w, "["+metadataFixture(3)+"]")
		default:
			fmt.Fprint(w, "[]")
		}
	})
	got, err := listReleaseMetadata(context.Background(), client)
	if err != nil || len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("unexpected observations: %v, %v", got, err)
	}
}
