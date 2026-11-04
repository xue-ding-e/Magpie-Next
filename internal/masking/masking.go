// Package masking is Settings → Privacy's masking as the gateway's masker
// (gwmw.Masker): secrets, personal data and the user's own words go to a
// vendor as placeholders (internal/redact), and come back as they were.
// It reads the settings on each call. plugins/redact serves it.
package masking

import (
	"net/http"

	"github.com/yetone/magpie/internal/gwmw"
	"github.com/yetone/magpie/internal/localdictionary"
	"github.com/yetone/magpie/internal/pii"
	"github.com/yetone/magpie/internal/redact"
	"github.com/yetone/magpie/internal/settings"
)

// Masker masks as the settings say, read on each call.
type Masker struct{ scoped *redact.Options }

// NewRequest fixes one dictionary generation and budget for multi-field requests.
func (Masker) NewRequest() gwmw.Masker {
	o := redact.DictionaryRequest(options())
	return Masker{scoped: &o}
}
func (m Masker) options() redact.Options {
	if m.scoped != nil {
		return *m.scoped
	}
	return options()
}

// Wanted says settings st ask for anything to be masked.
func Wanted(st settings.Settings) bool {
	return st.Redact || st.RedactPersonal || len(st.RedactWords) > 0 || localdictionary.Wanted()
}

func options() redact.Options {
	st := settings.Shared()
	dictionary, err := localdictionary.Matcher()
	if err != nil {
		panic(err)
	} // gwmw.Safely holds the request back; never silently skip protection.
	o := redact.Options{Secrets: st.Redact, Personal: st.RedactPersonal, Kinds: st.RedactKinds, Words: st.RedactWords, Dictionary: dictionary, Rules: st.RedactRules}
	if st.RedactModel {
		o.Model = model
	}
	return o
}

// model is the local model (internal/pii) as redact's Model. One that
// can't answer panics, and the request is held back (gwmw.Safely) rather
// than sent with only the rules' masking: the user asked for the model's.
func model(texts []string) [][]redact.Found {
	got, err := pii.Find(texts)
	if err != nil {
		panic(err)
	}
	return got
}

func (m Masker) JSON(body []byte) ([]byte, int) {
	o := m.options()
	if !o.Secrets && !o.Personal && len(o.Words) == 0 && o.Dictionary == nil {
		return body, 0
	}
	return redact.MaskJSON(body, o)
}

func (m Masker) Text(s string) (string, int) { return redact.Mask(s, m.options()) }

// JSONHits is JSON, with what it masked (gwmw.Teller).
func (m Masker) JSONHits(body []byte) ([]byte, []gwmw.Hit) {
	o := m.options()
	if !o.Secrets && !o.Personal && len(o.Words) == 0 && o.Dictionary == nil {
		return body, nil
	}
	var hits []gwmw.Hit
	o.Seen = told(&hits)
	out, _ := redact.MaskJSON(body, o)
	return out, hits
}

// TextHits is Text, with what it masked.
func (m Masker) TextHits(s string) (string, []gwmw.Hit) {
	o := m.options()
	var hits []gwmw.Hit
	o.Seen = told(&hits)
	out, _ := redact.Mask(s, o)
	return out, hits
}

func (Masker) Value(p string) (string, bool) { return redact.Value(p) }

// told adds each value masked to hits, with its hint: the value itself
// stays in redact's memory.
func told(hits *[]gwmw.Hit) func(redact.Hit) {
	return func(h redact.Hit) {
		v, _ := redact.Value(h.Placeholder)
		head, n := redact.Hint(h.Kind, v)
		if h.Kind == "LOCAL_TERM" {
			head = ""
		} // dictionary passwords never become activity-preview hints.
		*hits = append(*hits, gwmw.Hit{Kind: h.Kind, By: h.By, Placeholder: h.Placeholder, Head: head, Len: n})
	}
}

func (Masker) Restore(s string, escaped bool) string { return redact.Restore(s, escaped) }

func (Masker) Writer(w http.ResponseWriter) (http.ResponseWriter, func()) {
	rw := redact.NewWriter(w)
	return rw, rw.Finish
}

var _ gwmw.Teller = Masker{}
