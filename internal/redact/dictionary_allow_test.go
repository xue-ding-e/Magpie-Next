package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDictionaryExceptionKeepsBareToolArgumentsValid(t *testing.T) {
	const whole = "1234567890123456"
	body := []byte(`{"input":[{"type":"function_call","arguments":"{\"private_code\":1234567890123456}"}]}`)
	out, count := MaskJSON(body, Options{Dictionary: allowTestDictionary(t, whole), AllowDictionary: allowTestDictionary(t, "7890")})
	var value struct {
		Input []struct {
			Arguments string `json:"arguments"`
		} `json:"input"`
	}
	if json.Unmarshal(out, &value) != nil || len(value.Input) != 1 || !json.Valid([]byte(value.Input[0].Arguments)) {
		t.Fatal("splitting a bare JSON value around an exception broke tool arguments")
	}
	if count != 2 || !strings.Contains(value.Input[0].Arguments, "7890") || strings.Contains(value.Input[0].Arguments, "123456") {
		t.Fatalf("bare-value exception or masking failed: count=%d", count)
	}
}

func allowTestDictionary(t *testing.T, words ...string) *Dictionary {
	t.Helper()
	d, err := CompileDictionary(words)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDictionaryExceptionWinsAndKeepsNeighborProtected(t *testing.T) {
	const visible = "synthetic-visible"
	const hidden = "synthetic-neighbor-secret"
	d := allowTestDictionary(t, visible, hidden)
	allow := allowTestDictionary(t, visible)
	text := visible + " " + hidden
	out, n := Mask(text, Options{Dictionary: d, AllowDictionary: allow})
	if n != 1 || !strings.Contains(out, visible) || strings.Contains(out, hidden) || Restore(out, false) != text {
		t.Fatalf("exception or neighboring protection failed: count=%d", n)
	}
}

func TestDictionaryExceptionSubtractsOnlyItsOwnOccurrence(t *testing.T) {
	const text = "left-secret|保留🔑|right-secret"
	allow := allowTestDictionary(t, "保留", "留🔑")
	out, n := Mask(text, Options{Dictionary: allowTestDictionary(t, text), AllowDictionary: allow})
	if n != 2 || !strings.Contains(out, "保留🔑") || strings.Contains(out, "left-secret") ||
		strings.Contains(out, "right-secret") || Restore(out, false) != text {
		t.Fatalf("an overlapping exception released bytes outside itself: count=%d", n)
	}
}

func TestDictionaryExceptionOverridesRulesAndKnownValues(t *testing.T) {
	secret := "sk-proj-" + strings.Repeat("synthetic12", 4)
	masked, n := Mask(secret, Options{Secrets: true})
	if n != 1 || masked == secret {
		t.Fatal("fixture was not detected as an API key")
	}
	out, n := Mask(secret, Options{Secrets: true, AllowDictionary: allowTestDictionary(t, secret)})
	if out != secret || n != 0 {
		t.Fatal("a rule or previously known value defeated the explicit exception")
	}
}

func TestDictionaryExceptionIsExactAndCaseSensitive(t *testing.T) {
	const lower = "padded literal"
	const exact = " Padded Literal "
	text := lower + "|" + exact
	o := Options{Dictionary: allowTestDictionary(t, lower, exact), AllowDictionary: allowTestDictionary(t, exact)}
	out, n := Mask(text, o)
	if n != 1 || !strings.Contains(out, exact) || strings.Contains(out, lower) || Restore(out, false) != text {
		t.Fatal("exception trimming or case folding changed the explicit literal")
	}
}

func TestDictionaryExceptionsShareProtectionBudget(t *testing.T) {
	defer func() {
		if got := recover(); got != ErrDictionaryBudget {
			t.Fatalf("dense exception input must fail closed: %v", got)
		}
	}()
	Mask(strings.Repeat("x", MaxDictionaryMatches+1),
		Options{Words: []string{"xx"}, AllowDictionary: allowTestDictionary(t, "x")})
}

func TestDictionaryExceptionDoesNotRewriteAllowedSignedLiteral(t *testing.T) {
	const text = "synthetic-signed-public"
	o := dictionaryOptions(Options{Dictionary: allowTestDictionary(t, text), AllowDictionary: allowTestDictionary(t, text)}, 0)
	out, n := maskSignedDictionary(text, o)
	if n != 0 || out != text {
		t.Fatal("an explicitly allowed literal was rewritten in signed text")
	}
}
