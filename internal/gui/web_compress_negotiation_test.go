package gui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A compression-library update must not silently add a representation whose
// validator webCompressed cannot translate. These requests use only in-memory
// handlers, not a real listener, browser profile, or application configuration.
func TestWebCompressionKeepsGzipContract(t *testing.T) {
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
		{"browser offers zstd", "gzip, deflate, br, zstd", "gzip"},
		{"zstd only", "zstd", ""},
		{"no encoding", "", ""},
		{"gzip rejected", "gzip;q=0, identity;q=1", ""},
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
			got := rec.Body.Bytes()
			wantTag := baseTag
			if tt.encoding == "gzip" {
				reader, err := gzip.NewReader(bytes.NewReader(got))
				if err != nil {
					t.Fatal(err)
				}
				got, err = io.ReadAll(reader)
				closeErr := reader.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("decode=%v close=%v", err, closeErr)
				}
				wantTag = `"compression-contract-gzip"`
			}
			if string(got) != body || rec.Header().Get("ETag") != wantTag {
				t.Fatalf("body equal=%v tag=%q, want %q", string(got) == body, rec.Header().Get("ETag"), wantTag)
			}
			if again := request(wantTag); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
				t.Fatalf("conditional response=%d/%d bytes, want 304/0", again.Code, again.Body.Len())
			}
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
	}
}
