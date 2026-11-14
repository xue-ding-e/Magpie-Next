package redact

import (
	"fmt"
	"strings"
	"testing"
)

func TestLocalDictionaryMaskIntegration(t *testing.T) {
	d, e := CompileDictionary([]string{"bcde", "de", "x", "密码", " a,b "})
	if e != nil {
		t.Fatal(e)
	}
	out, n := Mask("abcde", Options{Words: []string{"abc"}, Dictionary: d})
	if n != 2 || Restore(out, false) != "abcde" {
		t.Fatalf("overlap/restore n=%d", n)
	}
	for _, v := range []string{"x", "密码", " a,b "} {
		out, n := Mask(v, Options{Dictionary: d})
		if n != 1 || (len(v) > 2 && strings.Contains(out, v)) || Restore(out, false) != v {
			t.Fatalf("literal masking failed n=%d", n)
		}
	}
	if head, _ := Hint("LOCAL_TERM", "synthetic-password"); head != "" {
		t.Fatal("exposed a password hint")
	}
	long := "synthetic-removed-password"
	d, _ = CompileDictionary([]string{long})
	Mask(long, Options{Dictionary: d})
	out, _ = Mask(long, Options{Secrets: true})
	if out != long {
		t.Fatal("removed dictionary literal stayed in known-secret index")
	}
}

// Same text, same dictionary and same Mask entrypoint. Compilation is outside
// the timed region, matching per-generation reuse; import peak is separate.
func BenchmarkLocalDictionaryComparison(b *testing.B) {
	words := make([]string, 100000)
	for i := range words {
		words[i] = fmt.Sprintf("synthetic-secret-%08d", i)
	}
	d, e := CompileDictionary(words)
	if e != nil {
		b.Fatal(e)
	}
	text := strings.Repeat("ordinary text without a match. ", 2048)
	for _, c := range []struct {
		name    string
		options Options
	}{{"legacy", Options{Words: words}}, {"compiled", Options{Dictionary: d}}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, n := Mask(text, c.options)
				if n != 0 || out != text {
					b.Fatal("unexpected change")
				}
			}
		})
	}
}
func TestLocalDictionaryKeepsLegacyShortInputSemantics(t *testing.T) {
	for _, words := range [][]string{nil, {"unrelated"}} {
		d, e := CompileDictionary(words)
		if e != nil {
			t.Fatal(e)
		}
		out, n := Mask("ab", Options{Words: []string{"ab"}, Dictionary: d})
		if out != "ab" || n != 0 {
			t.Fatal("local dictionary changed legacy short-input behavior")
		}
	}
}
func TestLocalDictionaryUnknownPlaceholderDoesNotBypass(t *testing.T) {
	d, e := CompileDictionary([]string{"PRIVATE", "password"})
	if e != nil {
		t.Fatal(e)
	}
	for _, in := range []string{"{{PRIVATE_abcdefgh}}", "{{TERM_password}}"} {
		out, n := Mask(in, Options{Dictionary: d})
		if n == 0 || out == in {
			t.Fatal("unknown placeholder bypassed library")
		}
		if Restore(out, false) != in {
			t.Fatal("literal restoration changed input")
		}
	}
	d, e = CompileDictionary([]string{"LOCAL_TERM", "synthetic-value"})
	if e != nil {
		t.Fatal(e)
	}
	out, _ := Mask("synthetic-value", Options{Dictionary: d})
	again, n := Mask(out, Options{Dictionary: d})
	if again != out || n != 0 {
		t.Fatal("local placeholder lost idempotence")
	}
}
func TestLocalDictionaryDenseBudgetFailsClosed(t *testing.T) {
	d, e := CompileDictionary([]string{"x"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if r := recover(); r != ErrDictionaryBudget {
			t.Fatalf("wanted safe budget rejection, got %v", r)
		}
	}()
	Mask(strings.Repeat("x", MaxDictionaryMatches+1), Options{Dictionary: d})
	t.Fatal("dense request was returned")
}
func TestLocalDictionaryJSONSharedBudget(t *testing.T) {
	d, e := CompileDictionary([]string{"x"})
	if e != nil {
		t.Fatal(e)
	}
	body := `{"messages":[{"role":"user","content":[` + strings.Repeat(`{"type":"text","text":"x"},`, MaxDictionaryMatches) + `{"type":"text","text":"x"}]}]}`
	defer func() {
		if r := recover(); r != ErrDictionaryBudget {
			t.Fatalf("wanted shared budget rejection, got %v", r)
		}
	}()
	MaskJSON([]byte(body), Options{Dictionary: d})
	t.Fatal("many small strings bypassed budget")
}
func TestLocalDictionaryBudgetBoundary(t *testing.T) {
	d, e := CompileDictionary([]string{"x"})
	if e != nil {
		t.Fatal(e)
	}
	out, n := Mask(strings.Repeat("x", MaxDictionaryMatches), Options{Dictionary: d})
	if n != MaxDictionaryMatches || len(out) > MaxDictionaryGrowthBytes {
		t.Fatal("boundary did not remain bounded", n, len(out))
	}
	if got := d.Spans(strings.Repeat("z", 64<<10)); len(got) != 0 {
		t.Fatal("no-hit text changed")
	}
	if allocs := testing.AllocsPerRun(3, func() { d.Spans("ordinary harmless data") }); allocs != 0 {
		t.Fatal("no-hit scan allocated", allocs)
	}
}
func TestLocalDictionaryInputBudget(t *testing.T) {
	d, e := CompileDictionary([]string{"long synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if r := recover(); r != ErrDictionaryBudget {
			t.Fatalf("wanted input budget rejection, got %v", r)
		}
	}()
	dictionaryOptions(Options{Dictionary: d}, MaxDictionaryTextBytes+1)
	t.Fatal("oversized input accepted")
}
func BenchmarkLocalDictionaryDenseRejected(b *testing.B) {
	d, e := CompileDictionary([]string{"x"})
	if e != nil {
		b.Fatal(e)
	}
	text := strings.Repeat("x", 64<<10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		func() {
			defer func() {
				if r := recover(); r != ErrDictionaryBudget {
					b.Fatalf("unexpected result %v", r)
				}
			}()
			Mask(text, Options{Dictionary: d})
		}()
	}
}
func TestLocalDictionarySignedReplay(t *testing.T) {
	const word = "synthetic-private-only-dictionary"
	d, e := CompileDictionary([]string{word})
	if e != nil {
		t.Fatal(e)
	}
	p, _ := Mask(word, Options{Dictionary: d})
	vendor := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + p + `","signature":"synthetic-signature"}]}],"call_id":"call_fixture"}`)
	replay := RestoreJSON(vendor)
	if !strings.Contains(string(replay), word) {
		t.Fatal("fixture did not restore")
	}
	out, n := MaskJSON(replay, Options{Dictionary: d, Secrets: true})
	if n == 0 || string(out) != string(vendor) {
		t.Fatal("signed roundtrip changed bytes or exposed literal")
	}
}
func TestLocalDictionarySignedUnknownFailsClosed(t *testing.T) {
	const word = "unseen-signed-synthetic-private"
	d, e := CompileDictionary([]string{word})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if r := recover(); r != ErrDictionarySigned {
			t.Fatalf("expected signed provenance rejection, got %v", r)
		}
	}()
	MaskJSON([]byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"`+word+`","signature":"synthetic"}]}]}`), Options{Dictionary: d})
	t.Fatal("unknown signed literal sent")
}
func TestLocalDictionarySignedOverlapUsesWinner(t *testing.T) {
	d, e := CompileDictionary([]string{"synthetic-abcde", "cde"})
	if e != nil {
		t.Fatal(e)
	}
	p, _ := Mask("synthetic-abcde", Options{Dictionary: d})
	vendor := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + p + `","signature":"synthetic-signature"}]}]}`)
	out, _ := MaskJSON(RestoreJSON(vendor), Options{Dictionary: d})
	if string(out) != string(vendor) {
		t.Fatal("covered suffix changed signed replay")
	}
}

func TestLocalDictionarySignedReplayKeepsLegacyWinner(t *testing.T) {
	const part = "synthetic-private-part"
	const value = "prefix-" + part
	for _, remembered := range []bool{false, true} {
		t.Run(fmt.Sprint(remembered), func(t *testing.T) {
			d, err := CompileDictionary([]string{part})
			if err != nil {
				t.Fatal(err)
			}
			if remembered {
				Mask(part, Options{Dictionary: d})
			}
			p, _ := Mask(value, Options{Words: []string{value}})
			vendor := []byte(`{"messages":[{"role":"user","content":"` + p + `"},{"role":"assistant","content":[{"type":"thinking","thinking":"` + p + `","signature":"synthetic-signature"}]}]}`)
			out, _ := MaskJSON(RestoreJSON(vendor), Options{Words: []string{value}, Dictionary: d})
			if string(out) != string(vendor) {
				t.Fatal("dictionary suffix changed the signed legacy placeholder")
			}
		})
	}
}

func TestLocalDictionarySignedReplayKeepsKnownSecret(t *testing.T) {
	const part = "synthetic-known-private-part"
	const value = "prefix-" + part
	d, err := CompileDictionary([]string{part})
	if err != nil {
		t.Fatal(err)
	}
	Mask(part, Options{Dictionary: d})
	p := placeholder("SECRET", value)
	vendor := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"` + p + `","signature":"synthetic-signature"}]}]}`)
	out, _ := MaskJSON(RestoreJSON(vendor), Options{Secrets: true, Dictionary: d})
	if string(out) != string(vendor) {
		t.Fatal("dictionary suffix changed the signed known-secret placeholder")
	}
}

// Near-limit headroom lets a small synthetic image expose accidental charging
// of the whole JSON body, without allocating a real 32 MiB image fixture.
func TestLocalDictionaryBudgetExcludesImageData(t *testing.T) {
	d, e := CompileDictionary([]string{"local-fixture"})
	if e != nil {
		t.Fatal(e)
	}
	o := DictionaryRequest(Options{Dictionary: d})
	o.dictionaryBudget.input = MaxDictionaryTextBytes - 128
	body := `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", 2048) + `"}},{"type":"text","text":"harmless"}]}]}`
	out, n := MaskJSON([]byte(body), o)
	if n != 0 || string(out) != body {
		t.Fatal("exempt image changed")
	}
	if o.dictionaryBudget.input >= MaxDictionaryTextBytes {
		t.Fatal("image consumed scanning budget")
	}
}
func TestLocalDictionaryBudgetCountsOnlyScannedStrings(t *testing.T) {
	d, e := CompileDictionary([]string{"local-fixture"})
	if e != nil {
		t.Fatal(e)
	}
	o := DictionaryRequest(Options{Dictionary: d})
	o.dictionaryBudget.input = MaxDictionaryTextBytes - 10
	defer func() {
		if r := recover(); r != ErrDictionaryBudget {
			t.Fatalf("scanned-string budget not enforced: %v", r)
		}
	}()
	MaskJSON([]byte(`{"messages":[{"role":"user","content":"abcdef"},{"role":"user","content":"ghijkl"}]}`), o)
	t.Fatal("separate strings bypassed input budget")
}
