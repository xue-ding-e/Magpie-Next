package gateway

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// These APIs use the media sender directly rather than attempt or
// forwardOnce. Only the allowed final model may reach their upstream.
func TestModelPolicyMediaJSONPosts(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	allowOnly(t, "gpt-6.1-sol")
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"id":"media-ok"}`)
	}))
	t.Cleanup(up.Close)
	var p = provider.Provider{ID: "media", Key: "key", Chat: up.URL, Responses: up.URL}
	s := New()
	for _, path := range []string{"/images/generations", "/images/edits", "/chat/completions", "/videos", "/videos/generations", "/contents/generations/tasks"} {
		before := calls.Load()
		if _, code, err := s.send(t.Context(), p, up.URL+path, "application/json", []byte(`{"model":"gpt-6-sol","prompt":"hi"}`), true); err == nil || code != http.StatusForbidden {
			t.Fatalf("%s allowed a forbidden model: status %d, %v", path, code, err)
		}
		if calls.Load() != before {
			t.Fatalf("%s sent the forbidden model upstream", path)
		}
		if _, code, err := s.send(t.Context(), p, up.URL+path, "application/json", []byte(`{"model":"gpt-6.1-sol","prompt":"hi"}`), true); err != nil || code != http.StatusOK {
			t.Fatalf("%s rejected the allowed model: status %d, %v", path, code, err)
		}
	}
	before := calls.Load()
	if _, code, err := s.sendAs(t.Context(), p, http.MethodGet, up.URL+"/videos/old-video", "", nil, true); err != nil || code != http.StatusOK || calls.Load() != before+1 {
		t.Fatalf("GET of an existing video was blocked: status %d, %v", code, err)
	}
}

func mediaPolicyForm(t *testing.T, models ...string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	file, err := w.CreateFormFile("file", "audio.webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("audio bytes")); err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if err := w.WriteField("model", model); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), w.FormDataContentType()
}

func TestModelPolicyRetrievalPosts(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	allowOnly(t, "gpt-6.1-sol")
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(up.Close)
	var p = provider.Provider{ID: "retrieval", Key: "key", Chat: up.URL}
	s := New()
	for _, path := range []string{"/embeddings", "/rerank", "/audio/speech"} {
		before := calls.Load()
		if _, _, code, err := s.retrieveAs(t.Context(), p, up.URL+path, "application/json", []byte(`{"model":"gpt-6-sol","input":"hi"}`)); err == nil || code != http.StatusForbidden || calls.Load() != before {
			t.Fatalf("%s sent a forbidden model: status %d, %v", path, code, err)
		}
		if _, _, code, err := s.retrieveAs(t.Context(), p, up.URL+path, "application/json", []byte(`{"model":"gpt-6.1-sol","input":"hi"}`)); err != nil || code != http.StatusOK {
			t.Fatalf("%s rejected the allowed model: status %d, %v", path, code, err)
		}
	}
	for _, models := range [][]string{{"gpt-6-sol"}, {"gpt-6-sol", "gpt-6.1-sol"}, nil} {
		body, ctype := mediaPolicyForm(t, models...)
		before := calls.Load()
		for _, path := range []string{"/audio/transcriptions", "/audio/translations"} {
			if _, _, code, err := s.retrieveAs(t.Context(), p, up.URL+path, ctype, body); err == nil || code != http.StatusForbidden || calls.Load() != before {
				t.Fatalf("%s accepted multipart models %v: status %d, %v", path, models, code, err)
			}
		}
		if _, code, err := s.send(t.Context(), p, up.URL+"/images/edits", ctype, body, true); err == nil || code != http.StatusForbidden || calls.Load() != before {
			t.Fatalf("image edit accepted multipart models %v: status %d, %v", models, code, err)
		}
	}
	body, ctype := mediaPolicyForm(t, "gpt-6.1-sol")
	if _, _, code, err := s.retrieveAs(t.Context(), p, up.URL+"/audio/transcriptions", ctype, body); err != nil || code != http.StatusOK {
		t.Fatalf("allowed multipart request: status %d, %v", code, err)
	}
}

func TestModelPolicyMediaURLAndUnset(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	allowOnly(t, "gpt-6.1-sol")
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"candidates":[]}`)
	}))
	t.Cleanup(up.Close)
	var p = provider.Provider{ID: "google-media", Key: "key", Chat: up.URL}
	s := New()
	for _, path := range []string{"/v1beta/models/", "/v1/projects/demo/locations/global/publishers/google/models/"} {
		before := calls.Load()
		if _, code, err := s.send(t.Context(), p, up.URL+path+"gpt-6-sol:generateContent", "application/json", []byte(`{"model":"gpt-6.1-sol","contents":[]}`), false); err == nil || code != http.StatusForbidden || calls.Load() != before {
			t.Fatalf("forbidden Gemini URL was sent: status %d, %v", code, err)
		}
		if _, code, err := s.send(t.Context(), p, up.URL+path+"gpt-6.1-sol:generateContent", "application/json", []byte(`{"contents":[]}`), false); err != nil || code != http.StatusOK {
			t.Fatalf("allowed Gemini URL was blocked: status %d, %v", code, err)
		}
	}
	saved := settings.Load()
	saved.AllowedModels = nil
	if err := settings.Save(saved); err != nil {
		t.Fatal(err)
	}
	if _, code, err := s.send(t.Context(), p, up.URL+"/images/generations", "application/json", []byte(`{"prompt":"vendor chooses"}`), true); err != nil || code != http.StatusOK {
		t.Fatalf("unset policy changed the old media path: status %d, %v", code, err)
	}
}
