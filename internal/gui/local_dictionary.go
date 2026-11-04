package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/yetone/magpie/internal/localdictionary"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"
)

// This registers inside the existing authenticated, same-origin GUI API, not
// a new listener. Metadata is safe by default; entries require explicit reveal.
func localDictionaryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/local-dictionary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		s, e := localdictionary.Read(offset, limit, r.URL.Query().Get("reveal") == "1")
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, s)
	})
	mux.HandleFunc("POST /api/settings/local-dictionary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var in struct {
			Revision *string `json:"revision"`
			Mode     *string `json:"mode"`
			Format   *string `json:"format"`
			Content  *string `json:"content"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, localdictionary.MaxEncodedBytes))
		if err != nil || !utf8.Valid(raw) {
			http.Error(w, "invalid or oversized UTF-8 dictionary request", http.StatusBadRequest)
			return
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if e := d.Decode(&in); e != nil {
			http.Error(w, "invalid or oversized dictionary request", http.StatusBadRequest)
			return
		}
		if e := d.Decode(new(any)); e != io.EOF {
			http.Error(w, "invalid dictionary request", http.StatusBadRequest)
			return
		}
		if in.Revision == nil || in.Mode == nil || in.Format == nil || in.Content == nil {
			http.Error(w, "dictionary request requires string fields", http.StatusBadRequest)
			return
		}
		s, e := localdictionary.Update(*in.Revision, *in.Mode, *in.Format, *in.Content)
		if errors.Is(e, localdictionary.ErrConflict) {
			http.Error(w, e.Error(), http.StatusConflict)
			return
		}
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, s)
	})
}
