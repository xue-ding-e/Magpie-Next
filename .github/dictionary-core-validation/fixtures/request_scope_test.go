package redact

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// One request's options are copied into concurrent fields: none may reset its
// reservation. Inputs are deliberately small; this is not a resource test.
func TestDictionaryValidationRequestScopeRace(t *testing.T) {
	d, err := CompileDictionary([]string{"validation-synthetic-literal"})
	if err != nil {
		t.Fatal(err)
	}
	options := DictionaryRequest(Options{Dictionary: d})
	var rejected, completed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					if r != ErrDictionaryBudget {
						t.Errorf("unexpected panic kind")
					}
					rejected.Add(1)
				}
			}()
			<-start
			_, n := Mask(strings.Repeat("validation-synthetic-literal ", 600), options)
			if n != 600 {
				t.Errorf("field did not mask all literals")
			}
			completed.Add(1)
		}()
	}
	close(start)
	wg.Wait()
	if rejected.Load() == 0 || completed.Load()+rejected.Load() != 8 {
		t.Fatal("request budget was reset across concurrent fields")
	}
	if _, n := Mask("validation-synthetic-literal", DictionaryRequest(Options{Dictionary: d})); n != 1 {
		t.Fatal("failed request contaminated the next request")
	}
}

func TestDictionaryValidationGrowthBoundary(t *testing.T) {
	d, err := CompileDictionary([]string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	options := DictionaryRequest(Options{Dictionary: d})
	options.dictionaryBudget.growth = MaxDictionaryGrowthBytes
	defer func() {
		if recover() != ErrDictionaryBudget {
			t.Error("placeholder growth did not fail closed")
		}
	}()
	Mask("fixture", options)
	t.Fatal("growth over the request cap was accepted")
}

// Legal opaque reasoning and tool identities survive exactly; only mappings
// this process actually created may be reconstructed inside signed payloads.
func TestDictionaryValidationSignedProtocols(t *testing.T) {
	const word = "validation-synthetic-signed-literal"
	d, err := CompileDictionary([]string{word})
	if err != nil {
		t.Fatal(err)
	}
	tag, _ := Mask(word, Options{Dictionary: d})
	for _, body := range []string{
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"VALUE","signature":"synthetic-signature"}]}],"call_id":"call_validation-synthetic-signed-literal"}`,
		`{"contents":[{"role":"model","parts":[{"text":"VALUE","thought":true,"thoughtSignature":"synthetic-signature"}]}],"call_id":"call_fixture"}`,
		`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"note","args":{"text":"VALUE"}},"thoughtSignature":"synthetic-signature"}]}],"call_id":"call_fixture"}`,
		`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"VALUE"}],"encrypted_content":"synthetic-opaque-reasoning"}],"call_id":"call_fixture"}`,
	} {
		vendor := strings.ReplaceAll(body, "VALUE", tag)
		restored := RestoreJSON([]byte(vendor))
		out, n := MaskJSON(restored, DictionaryRequest(Options{Dictionary: d, Secrets: true}))
		if n == 0 || string(out) != vendor {
			t.Fatal("known signed mapping or protocol identity was changed")
		}
		harmless := strings.ReplaceAll(body, "VALUE", "ordinary legal reasoning")
		if unchanged, _ := MaskJSON([]byte(harmless), DictionaryRequest(Options{Dictionary: d, Secrets: true})); string(unchanged) != harmless {
			t.Fatal("legal reasoning or call_id was changed")
		}
	}
}

func TestDictionaryValidationUnknownSignedProtocols(t *testing.T) {
	const word = "validation-unseen-synthetic-signed-literal"
	d, err := CompileDictionary([]string{word})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"VALUE","signature":"synthetic-signature"}]}]}`,
		`{"contents":[{"role":"model","parts":[{"text":"VALUE","thought":true,"thoughtSignature":"synthetic-signature"}]}]}`,
		`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"note","args":{"text":"VALUE"}},"thoughtSignature":"synthetic-signature"}]}]}`,
		`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"VALUE"}],"encrypted_content":"synthetic-opaque-reasoning"}]}`,
	} {
		func() {
			defer func() {
				if recover() != ErrDictionarySigned {
					t.Error("unknown signed mapping did not fail closed")
				}
			}()
			MaskJSON([]byte(strings.ReplaceAll(body, "VALUE", word)), DictionaryRequest(Options{Dictionary: d}))
			t.Error("unknown signed mapping returned a sendable body")
		}()
	}
}
