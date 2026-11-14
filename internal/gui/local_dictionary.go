package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/localdictionary"
)

// This registers inside the existing authenticated, same-origin GUI API, not
// a new listener. Metadata is safe by default; entries require explicit reveal.
func localDictionaryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/local-dictionary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !codexAuthOrigin(r, r.Header.Get("Origin")) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		target := r.URL.Query().Get("target")
		if target == "" {
			target = "mask"
		}
		if target != "mask" && target != "allow" {
			http.Error(w, "unknown dictionary list", http.StatusBadRequest)
			return
		}
		s, e := localdictionary.ReadList(target, offset, limit, r.URL.Query().Get("reveal") == "1")
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, s)
	})
	mux.HandleFunc("POST /api/settings/local-dictionary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Require the same native transport or production web cookie as
		// credential export. Simple forms cannot change the password library.
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if media != "application/json" || !codexAuthOrigin(r, r.Header.Get("Origin")) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		var in struct {
			Revision *string                    `json:"revision"`
			Mode     *string                    `json:"mode"`
			Format   *string                    `json:"format"`
			Content  *string                    `json:"content"`
			Target   string                     `json:"target"`
			Offset   *int                       `json:"offset"`
			Count    *int                       `json:"count"`
			Words    *localdictionary.PageWords `json:"words"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, localdictionary.MaxImportEncodedBytes))
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
		if in.Target == "" {
			in.Target = "mask"
		}
		if in.Target != "mask" && in.Target != "allow" {
			http.Error(w, "unknown dictionary list", http.StatusBadRequest)
			return
		}
		var s localdictionary.State
		var e error
		if *in.Mode == "edit" {
			if *in.Format != "json" || *in.Content != "" || in.Offset == nil || in.Count == nil || in.Words == nil {
				http.Error(w, "page editing requires JSON entries, offset and count", http.StatusBadRequest)
				return
			}
			s, e = localdictionary.UpdatePage(in.Target, *in.Revision, *in.Offset, *in.Count, *in.Words)
		} else {
			if in.Offset != nil || in.Count != nil || in.Words != nil {
				http.Error(w, "page fields require edit mode", http.StatusBadRequest)
				return
			}
			s, e = localdictionary.UpdateList(in.Target, *in.Revision, *in.Mode, *in.Format, *in.Content)
		}
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
