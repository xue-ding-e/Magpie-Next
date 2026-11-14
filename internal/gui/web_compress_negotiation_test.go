package gui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// A compression-library update must not silently add a representation whose
// validator webCompressed cannot translate. These requests use only in-memory
// handlers, not a real listener, browser profile, or application configuration.
func TestWebCompressionNegotiates(t *testing.T) {
	const baseTag = `"compression-contract"`
	body := strings.Repeat("synthetic web response\n", 256)
	handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("ETag", baseTag)
		if r.Header.Get("If-None-Match") == baseTag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	var cases = []struct {
		name     string
		accept   string
		encoding string
	}{
		{"gzip", "gzip", "gzip"},
		{"browser offers zstd", "gzip, deflate, br, zstd", "zstd"},
		{"zstd only", "zstd", "zstd"},
		{"gzip preferred", "gzip;q=1, zstd;q=0.5", "gzip"},
		{"zstd preferred", "gzip;q=0.5, zstd;q=1", "zstd"},
		{"equal quality", "gzip;q=0.5, zstd;q=0.5", "zstd"},
		{"gzip fallback", "gzip, zstd;q=0", "gzip"},
		{"zstd fallback", "gzip;q=0, zstd", "zstd"},
		{"no encoding", "", ""},
		{"identity", "identity", ""},
		{"unsupported", "br, deflate", ""},
		{"gzip rejected", "gzip;q=0, identity;q=1", ""},
		{"both rejected", "gzip;q=0, zstd;q=0, identity;q=1", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request := func(tag string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, "/synthetic.txt", nil)
				r.Header.Set("Accept-Encoding", tt.accept)
				if tag != "" {
					r.Header.Set("If-None-Match", tag)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, r)
				return rec
			}
			rec := request("")
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != tt.encoding {
				t.Fatalf("status=%d encoding=%q, want 200/%q", rec.Code, rec.Header().Get("Content-Encoding"), tt.encoding)
			}
			got := webCompressionDecoded(t, rec)
			wantTag := baseTag
			if tt.encoding != "" {
				wantTag = `"compression-contract-` + tt.encoding + `"`
			}
			if string(got) != body || rec.Header().Get("ETag") != wantTag {
				t.Fatalf("body equal=%v tag=%q, want %q", string(got) == body, rec.Header().Get("ETag"), wantTag)
			}
			if again := request(wantTag); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
				t.Fatalf("conditional response=%d/%d bytes, want 304/0", again.Code, again.Body.Len())
			} else {
				webCompressionNotModified(t, again, wantTag)
			}
			webCompressionVary(t, rec)
		})
	}
}

// Do not assume an encoding here: whichever representation was negotiated must
// recognize its own validator on the next request. This catches an added coding
// whose suffix webCompressed does not translate, rather than only an encoding
// preference change.
func TestWebCompressionRevalidatesNegotiatedRepresentation(t *testing.T) {
	const baseTag = `"negotiated-representation"`
	body := strings.Repeat("synthetic conditional response\n", 256)
	handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("ETag", baseTag)
		if r.Header.Get("If-None-Match") == baseTag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	request := func(tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/synthetic.txt", nil)
		r.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
		if tag != "" {
			r.Header.Set("If-None-Match", tag)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	first := request("")
	if first.Code != http.StatusOK || first.Body.Len() == 0 || first.Header().Get("ETag") == "" {
		t.Fatalf("first response status=%d bytes=%d tag=%q", first.Code, first.Body.Len(), first.Header().Get("ETag"))
	}
	if next := request(first.Header().Get("ETag")); next.Code != http.StatusNotModified || next.Body.Len() != 0 {
		t.Fatalf("%s representation %s revalidated as %d/%d bytes, want 304/0", first.Header().Get("Content-Encoding"), first.Header().Get("ETag"), next.Code, next.Body.Len())
	} else {
		webCompressionNotModified(t, next, first.Header().Get("ETag"))
	}
}

// A cache holds bytes of one representation. Switching coding must send the
// newly selected bytes, even if all three representations share a source file.
func TestWebCompressionSwitchesRepresentation(t *testing.T) {
	const baseTag = `"switch-representation"`
	body := strings.Repeat("synthetic representation switch\n", 256)
	handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("ETag", baseTag)
		w.Header().Add("Vary", "Origin")
		http.ServeContent(w, r, "synthetic.txt", time.Time{}, strings.NewReader(body))
	}))
	var encodings = []string{"identity", "gzip", "zstd"}
	for _, from := range encodings {
		first := webCompressionGet(handler, from, "")
		for _, to := range encodings {
			t.Run(from+" to "+to, func(t *testing.T) {
				want := webCompressionGet(handler, to, "")
				next := webCompressionGet(handler, to, first.Header().Get("ETag"))
				if vary := strings.Join(next.Result().Header.Values("Vary"), ", "); !strings.Contains(vary, "Origin") {
					t.Fatalf("lost the handler's Vary: %q", vary)
				}
				if from == to {
					webCompressionNotModified(t, next, want.Header().Get("ETag"))
					return
				}
				if next.Code != http.StatusOK || next.Header().Get("Content-Encoding") != want.Header().Get("Content-Encoding") ||
					next.Header().Get("ETag") != want.Header().Get("ETag") || string(webCompressionDecoded(t, next)) != body {
					t.Fatalf("switched response=%d encoding=%q tag=%q, want 200/%q/%q and full body",
						next.Code, next.Header().Get("Content-Encoding"), next.Header().Get("ETag"), want.Header().Get("Content-Encoding"), want.Header().Get("ETag"))
				}
				webCompressionVary(t, next)
			})
		}
	}
}

// The comparison is weak, accepts a list or *, and treats each opaque tag as
// a whole: commas and encoding suffixes can also be part of the source's tag.
func TestWebCompressionConditionalTags(t *testing.T) {
	for _, sourceTag := range []string{`"opaque,gzip-zstd"`, `W/"opaque,gzip-zstd"`} {
		for _, encoding := range []string{"identity", "gzip", "zstd"} {
			t.Run(sourceTag+"/"+encoding, func(t *testing.T) {
				body := strings.Repeat("synthetic conditional tags\n", 256)
				handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("ETag", sourceTag)
					http.ServeContent(w, r, "synthetic.txt", time.Time{}, strings.NewReader(body))
				}))
				tag := webCompressionGet(handler, encoding, "").Header().Get("ETag")
				strongTag := strings.TrimPrefix(tag, "W/")
				for _, condition := range []string{tag, strongTag, "W/" + strongTag, `"stale", W/` + strongTag, "*"} {
					webCompressionNotModified(t, webCompressionGet(handler, encoding, condition), tag)
				}
				req := httptest.NewRequest(http.MethodGet, "/synthetic.txt", nil)
				req.Header.Set("Accept-Encoding", encoding)
				req.Header.Add("If-None-Match", `"stale"`)
				req.Header.Add("If-None-Match", tag)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				webCompressionNotModified(t, rec, tag)
				if got := req.Header.Values("If-None-Match"); len(got) != 2 || got[0] != `"stale"` || got[1] != tag {
					t.Fatalf("changed the caller's validators: %q", got)
				}
				if rec := webCompressionGet(handler, encoding, `"stale-gzip", W/"stale-zstd"`); rec.Code != http.StatusOK || string(webCompressionDecoded(t, rec)) != body {
					t.Fatalf("stale validators: status=%d, want 200 and full body", rec.Code)
				}
			})
		}
	}
}

// Offering a coding is not enough to choose its validator: a small response,
// an event stream and bytes already encoded by a service all pass through.
func TestWebCompressionLeavesResponsesAlone(t *testing.T) {
	body := strings.Repeat("data: synthetic event\n\n", 256)
	var cases = []struct {
		name, contentType, encoding string
		body                        []byte
	}{
		{"small text", "text/plain", "", []byte("small response")},
		{"event stream", "text/event-stream; charset=utf-8", "", []byte(body)},
	}
	// Encoded fixtures must exceed the compression threshold, so the size
	// check cannot hide a compressor that ignores Content-Encoding.
	var serviceBody strings.Builder
	for i := range 4096 {
		serviceBody.WriteString("synthetic service line " + strconv.Itoa(i) + "\n")
	}
	for _, encoding := range []string{"gzip", "zstd"} {
		encoded := webCompressionGet(webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, serviceBody.String())
		})), encoding, "")
		if encoded.Header().Get("Content-Encoding") != encoding || encoded.Body.Len() < 2048 {
			t.Fatalf("fixture encoding=%q bytes=%d, want %s and at least 2048 bytes", encoded.Header().Get("Content-Encoding"), encoded.Body.Len(), encoding)
		}
		cases = append(cases, struct {
			name, contentType, encoding string
			body                        []byte
		}{"already " + encoding, "text/plain", encoding, encoded.Body.Bytes()})
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			// A suffix in an origin's opaque tag must not be stripped.
			const tag = `"service-gzip-zstd"`
			handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.Header().Set("Content-Encoding", tt.encoding)
				w.Header().Set("ETag", tag)
				http.ServeContent(w, r, "synthetic.txt", time.Time{}, bytes.NewReader(tt.body))
			}))
			for _, accept := range []string{"gzip", "zstd", "gzip, zstd"} {
				for _, condition := range []string{"", `"stale"`, tag + `, "stale"`} {
					rec := webCompressionGet(handler, accept, condition)
					if strings.HasPrefix(condition, tag) {
						webCompressionNotModified(t, rec, tag)
						continue
					}
					if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != tt.encoding ||
						rec.Header().Get("ETag") != tag || !bytes.Equal(rec.Body.Bytes(), tt.body) {
						t.Fatalf("Accept-Encoding=%q conditional=%q: status=%d encoding=%q tag=%q bytes equal=%v",
							accept, condition, rec.Code, rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"), bytes.Equal(rec.Body.Bytes(), tt.body))
					}
					webCompressionVary(t, rec)
				}
			}
		})
	}
}

// Flush must reach an open event stream even on the conditional-response path.
// Inspect the recorder before sending the next event, with no listener or timer.
func TestWebCompressionFlushesEventStream(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd", "gzip, zstd"} {
		for _, condition := range []string{"", `"stale"`} {
			t.Run(encoding+"/"+condition, func(t *testing.T) {
				rec := httptest.NewRecorder()
				handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: first\n\n")
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Fatal(err)
					}
					if !rec.Flushed || rec.Body.String() != "data: first\n\n" {
						t.Fatalf("first event held back: flushed=%v body=%q", rec.Flushed, rec.Body.String())
					}
					_, _ = io.WriteString(w, "data: second\n\n")
				}))
				req := httptest.NewRequest(http.MethodGet, "/synthetic.txt", nil)
				req.Header.Set("Accept-Encoding", encoding)
				req.Header.Set("If-None-Match", condition)
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "data: first\n\ndata: second\n\n" {
					t.Fatalf("event stream: status=%d encoding=%q body=%q", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
				}
			})
		}
	}
}

// Conditional reads still respect HEAD's identity representation and range
// responses. If-None-Match also takes precedence over If-Modified-Since.
func TestWebCompressionConditionalReads(t *testing.T) {
	const baseTag = `"conditional-read"`
	const since = "Mon, 02 Jan 2006 15:04:05 GMT"
	modified, err := http.ParseTime(since)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("synthetic conditional read\n", 256)
	handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", baseTag)
		http.ServeContent(w, r, "synthetic.txt", modified, strings.NewReader(body))
	}))
	var cases = []struct {
		name, method, accept, tag, since, byteRange string
		status                                      int
		wantTag, encoding, body                     string
	}{
		{"stale tag takes precedence", "GET", "gzip, zstd", `"stale"`, since, "", 200, `"conditional-read-zstd"`, "zstd", body},
		{"source tag takes precedence", "GET", "gzip, zstd", baseTag, since, "", 200, `"conditional-read-zstd"`, "zstd", body},
		{"matching tag takes precedence", "GET", "gzip, zstd", `"conditional-read-zstd"`, since, "", 304, `"conditional-read-zstd"`, "", ""},
		{"date alone", "GET", "gzip, zstd", "", since, "", 304, baseTag, "", ""},
		{"head identity", "HEAD", "gzip, zstd", baseTag, "", "", 304, baseTag, "", ""},
		{"head gzip tag", "HEAD", "gzip", `"conditional-read-gzip"`, "", "", 200, baseTag, "", ""},
		{"head zstd tag", "HEAD", "zstd", `"conditional-read-zstd"`, "", "", 200, baseTag, "", ""},
		{"range identity", "GET", "gzip, zstd", baseTag, "", "bytes=0-31", 304, baseTag, "", ""},
		{"range gzip tag", "GET", "gzip", `"conditional-read-gzip"`, "", "bytes=0-31", 206, baseTag, "", body[:32]},
		{"range zstd tag", "GET", "zstd", `"conditional-read-zstd"`, "", "bytes=0-31", 206, baseTag, "", body[:32]},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/synthetic.txt", nil)
			req.Header.Set("Accept-Encoding", tt.accept)
			req.Header.Set("If-None-Match", tt.tag)
			req.Header.Set("If-Modified-Since", tt.since)
			req.Header.Set("Range", tt.byteRange)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if tt.status == http.StatusNotModified {
				webCompressionNotModified(t, rec, tt.wantTag)
				if got := rec.Result().Header.Get("Content-Range"); got != "" {
					t.Errorf("304 has Content-Range=%q", got)
				}
				return
			}
			if rec.Code != tt.status || rec.Header().Get("ETag") != tt.wantTag ||
				rec.Header().Get("Content-Encoding") != tt.encoding || string(webCompressionDecoded(t, rec)) != tt.body {
				t.Fatalf("conditional read: status=%d tag=%q encoding=%q bytes=%d, want %d/%q/%q/%d bytes",
					rec.Code, rec.Header().Get("ETag"), rec.Header().Get("Content-Encoding"), rec.Body.Len(), tt.status, tt.wantTag, tt.encoding, len(tt.body))
			}
			webCompressionVary(t, rec)
		})
	}
}

// A validator must never turn an error or a failed precondition into success.
// Non-read methods keep their original precondition for the handler to enforce.
func TestWebCompressionKeepsStatusAndWriteConditions(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusServiceUnavailable} {
		handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", `"status"`)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "synthetic status response")
		}))
		if rec := webCompressionGet(handler, "gzip, zstd", "*"); rec.Code != status || rec.Body.String() != "synthetic status response" {
			t.Fatalf("status=%d body=%q, want %d and its body", rec.Code, rec.Body.String(), status)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		const tag = `"write-condition-gzip"`
		handler := webCompressed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("If-None-Match"); got != tag {
				t.Fatalf("%s precondition=%q, want %q", method, got, tag)
			}
			w.WriteHeader(http.StatusPreconditionFailed)
		}))
		req := httptest.NewRequest(method, "/synthetic.txt", nil)
		req.Header.Set("Accept-Encoding", "gzip, zstd")
		req.Header.Set("If-None-Match", tag)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusPreconditionFailed {
			t.Fatalf("%s status=%d, want 412", method, rec.Code)
		}
	}
}

func webCompressionGet(handler http.Handler, accept, tag string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/synthetic.txt", nil)
	req.Header.Set("Accept-Encoding", accept)
	if tag != "" {
		req.Header.Set("If-None-Match", tag)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func webCompressionDecoded(t *testing.T, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	switch rec.Header().Get("Content-Encoding") {
	case "":
		return rec.Body.Bytes()
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("decode=%v close=%v", err, closeErr)
		}
		return body
	case "zstd":
		reader, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		body, err := reader.DecodeAll(rec.Body.Bytes(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return body
	default:
		t.Fatalf("unexpected encoding %q", rec.Header().Get("Content-Encoding"))
		return nil
	}
}

func webCompressionVary(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, line := range rec.Result().Header.Values("Vary") {
		for field := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Accept-Encoding") {
				return
			}
		}
	}
	t.Fatalf("Vary=%q, want Accept-Encoding", rec.Result().Header.Values("Vary"))
}

func webCompressionNotModified(t *testing.T, rec *httptest.ResponseRecorder, tag string) {
	t.Helper()
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Result().Header.Get("ETag") != tag {
		t.Fatalf("conditional response=%d/%d bytes tag=%q, want 304/0/%q", rec.Code, rec.Body.Len(), rec.Result().Header.Get("ETag"), tag)
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Encoding"} {
		if value := rec.Result().Header.Get(header); value != "" {
			t.Errorf("304 has %s=%q", header, value)
		}
	}
	webCompressionVary(t, rec)
}
