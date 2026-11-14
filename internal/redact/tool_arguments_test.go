package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestMaskJSONToolArgumentsKeepJSON(t *testing.T) {
	const secret = "JsonBoundary-42!"
	arguments := `{"text":"` + secret + `","signature":"DB_PASSWORD=SignatureValue-63!"}`
	quoted, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, body, path string }{
		{"responses", `{"input":[{"type":"function_call","name":"shell","call_id":"call_test","arguments":` + string(quoted) + `}]}`, "input.0.arguments"},
		{"chat", `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_test","type":"function","function":{"name":"shell","arguments":` + string(quoted) + `}}]}]}`, "messages.0.tool_calls.0.function.arguments"},
		{"legacy chat", `{"messages":[{"role":"assistant","function_call":{"name":"shell","arguments":` + string(quoted) + `}}]}`, "messages.0.function_call.arguments"},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			model := func(texts []string) [][]Found {
				calls++
				out := make([][]Found, len(texts))
				for i, s := range texts {
					if start := strings.Index(s, secret); start >= 0 {
						// The real failure included the closing quote and brace
						// when the entire arguments document was sent as text.
						out[i] = []Found{{Start: start, End: len(s), Label: "secret"}}
					}
				}
				return out
			}
			out, _ := MaskJSON([]byte(c.body), Options{Secrets: true, Model: model})
			args := gjson.GetBytes(out, c.path).String()
			if !json.Valid([]byte(args)) {
				t.Fatalf("masking broke the tool's JSON arguments: %s", args)
			}
			if strings.Contains(args, secret) || strings.Contains(args, "SignatureValue-63!") {
				t.Fatalf("tool arguments leaked a secret: %s", args)
			}
			if calls != 1 {
				t.Fatalf("model calls = %d, want one batch per request", calls)
			}
			if restored := RestoreJSON(out); string(restored) != c.body {
				t.Fatalf("the original argument JSON did not round trip: %s", restored)
			}
		})
	}
}

func TestMaskJSONEncryptedToolArguments(t *testing.T) {
	// The reporter's opaque argument had 1572 ASCII bytes. No real sealed
	// messages or credentials are needed to reproduce the span boundary.
	cipher := "gAAAAA" + strings.Repeat("bX7_", 391) + "=="
	const secret = "SealedSibling-84!"
	declaration := `{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true},"note":{"type":"string"}}}}]}`
	arguments, err := json.Marshal(`{"message":"` + cipher + `","note":"DB_PASSWORD=` + secret + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	call := `{"type":"function_call","id":"fc_sealed","namespace":"collaboration","name":"followup_task","call_id":"call_sealed","encrypted_content":"gAAAAA_signed_call==","arguments":` + string(arguments) + `}`
	for _, c := range []struct{ name, body, path string }{
		{"tools", `{"tools":[` + declaration + `],"input":[` + call + `]}`, "input.0.arguments"},
		{"historical tools", `{"input":[{"type":"additional_tools","tools":[` + declaration + `]},` + call + `]}`, "input.1.arguments"},
		{"searched tools", `{"input":[{"type":"tool_search_output","tools":[` + declaration + `]},` + call + `]}`, "input.1.arguments"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked []string
			model := func(texts []string) [][]Found {
				asked = append(asked, texts...)
				out := make([][]Found, len(texts))
				for i, s := range texts {
					if start := strings.Index(s, cipher); start >= 0 {
						out[i] = []Found{{Start: start, End: len(s), Label: "secret"}}
					}
				}
				return out
			}
			out, _ := MaskJSON([]byte(c.body), Options{Secrets: true, Model: model})
			args := gjson.GetBytes(out, c.path).String()
			if !json.Valid([]byte(args)) {
				t.Fatalf("masking broke sealed arguments: %s", args)
			}
			if got := gjson.Get(args, "message").String(); got != cipher {
				t.Fatalf("opaque argument changed: got %d bytes, want %d", len(got), len(cipher))
			}
			if strings.Contains(args, secret) {
				t.Fatal("an unsealed sibling argument was left unmasked")
			}
			if strings.Contains(strings.Join(asked, "\n"), cipher) {
				t.Fatal("the privacy model was asked to classify opaque ciphertext")
			}
			again, _ := MaskJSON(out, Options{Secrets: true, Model: model})
			args = gjson.GetBytes(again, c.path).String()
			if !json.Valid([]byte(args)) || gjson.Get(args, "message").String() != cipher {
				t.Fatal("a second masking pass damaged the arguments")
			}
		})
	}

	t.Run("plaintext and nested lookalikes remain private", func(t *testing.T) {
		body := `{"tools":[` + declaration + `],"input":[{"type":"function_call","namespace":"collaboration","name":"followup_task","arguments":` + string(arguments) + `},{"type":"function_call","name":"shell","arguments":"{\"type\":\"function_call\",\"encrypted_content\":\"gAAAAA_fake==\",\"message\":\"DB_PASSWORD=` + secret + `\"}"}]}`
		var f fakeModel
		f.values = map[string]string{cipher: "secret"}
		out, _ := MaskJSON([]byte(body), Options{Secrets: true, Model: f.find})
		for _, path := range []string{"input.0.arguments", "input.1.arguments"} {
			args := gjson.GetBytes(out, path).String()
			if !json.Valid([]byte(args)) || strings.Contains(args, secret) || strings.Contains(args, cipher) {
				t.Fatalf("a protocol-shaped value bypassed masking: %s", args)
			}
		}
	})
}
