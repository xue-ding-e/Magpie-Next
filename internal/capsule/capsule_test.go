package capsule

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// Synthetic inputs only. Fixed nonces must never become a production writer.
func syntheticCapsule(t *testing.T, plain []byte) string {
	t.Helper()
	key := sha256.Sum256([]byte("ai-toolbox-remote-compaction"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("synthetic123")
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return prefix + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...))
}

func TestKnownSyntheticVector(t *testing.T) {
	// Independently generated with Python cryptography, not a Rust-run claim.
	value := "ai-toolbox-compact-v1:c3ludGhldGljMTIzlfbqVoqoUJaD8I7eKopBPyXv-z0g3WJrxDTKwYlDdvUl8tUy0EWv8VXm6As41o2CyzfgJdMC9MuL2rW87e12JWkHn3E1O9J1NWPeMomPE19kaFQSluzl"
	got, recognized, err := Open(value)
	if err != nil || !recognized || got != "Synthetic context only" {
		t.Fatal("independent synthetic vector was not decoded")
	}
}

func TestPayloadContract(t *testing.T) {
	cases := []struct {
		name, plain, want string
	}{
		{"normal", `{"summary":"context","model":"synthetic","created_at":1}`, "context"},
		{"whitespace_preserved", `{"summary":" \ncontext\t ","model":"","created_at":-1}`, " \ncontext\t "},
		{"unicode_whitespace_preserved", `{"summary":"\u0085context\u3000","model":"","created_at":0}`, "\u0085context\u3000"},
		{"non_bmp", `{"summary":"\ud83d\ude00","model":"","created_at":0}`, "😀"},
		{"escaped_literals", `{"summary":"literal \\ud800 and \"quoted\"","model":"","created_at":0}`, `literal \ud800 and "quoted"`},
		{"unknown", `{"extra":{"a":[null,true,3]},"summary":"context","model":"","created_at":0}`, "context"},
		{"unknown_duplicate", `{"extra":1,"extra":2,"summary":"context","model":"","created_at":0}`, "context"},
		{"unknown_large_exponent", `{"extra":1e400,"summary":"context","model":"","created_at":0}`, "context"},
		{"unknown_deep_array", `{"extra":` + strings.Repeat("[", 129) + "null" + strings.Repeat("]", 129) + `,"summary":"context","model":"","created_at":0}`, "context"},
		{"case_variant_ignored", `{"summary":"context","Summary":"other","MODEL":false,"model":"","created_at":0}`, "context"},
		{"escaped_field", `{"summ\u0061ry":"context","model":"","created_at":0}`, "context"},
		{"max_int64", `{"summary":"context","model":"","created_at":9223372036854775807}`, "context"},
		{"min_int64", `{"summary":"context","model":"","created_at":-9223372036854775808}`, "context"},
		{"empty", `{"summary":"","model":"","created_at":0}`, ""},
		{"blank", `{"summary":" \t\n\u0085\u3000","model":"","created_at":0}`, ""},
		{"case_only_summary", `{"Summary":"context","model":"","created_at":0}`, ""},
		{"case_only_model", `{"summary":"context","Model":"","created_at":0}`, ""},
		{"case_only_time", `{"summary":"context","model":"","Created_At":0}`, ""},
		{"duplicate_summary", `{"summary":"first","summary":"last","model":"","created_at":0}`, ""},
		{"escaped_duplicate", `{"summary":"first","summ\u0061ry":"last","model":"","created_at":0}`, ""},
		{"duplicate_model", `{"summary":"context","model":"a","model":"b","created_at":0}`, ""},
		{"duplicate_time", `{"summary":"context","model":"","created_at":0,"created_at":1}`, ""},
		{"missing_model", `{"summary":"context","created_at":0}`, ""},
		{"missing_summary", `{"model":"","created_at":0}`, ""},
		{"missing_time", `{"summary":"context","model":""}`, ""},
		{"null_summary", `{"summary":null,"model":"","created_at":0}`, ""},
		{"null_model", `{"summary":"context","model":null,"created_at":0}`, ""},
		{"boolean_model", `{"summary":"context","model":false,"created_at":0}`, ""},
		{"array_model", `{"summary":"context","model":[],"created_at":0}`, ""},
		{"null_time", `{"summary":"context","model":"","created_at":null}`, ""},
		{"number_summary", `{"summary":4,"model":"","created_at":0}`, ""},
		{"float_time", `{"summary":"context","model":"","created_at":1.0}`, ""},
		{"exponent_time", `{"summary":"context","model":"","created_at":1e0}`, ""},
		{"overflow_time", `{"summary":"context","model":"","created_at":9223372036854775808}`, ""},
		{"underflow_time", `{"summary":"context","model":"","created_at":-9223372036854775809}`, ""},
		{"max_u64_time", `{"summary":"context","model":"","created_at":18446744073709551615}`, ""},
		{"overflow_u64_time", `{"summary":"context","model":"","created_at":18446744073709551616}`, ""},
		{"negative_zero_time", `{"summary":"context","model":"","created_at":-0}`, ""},
		{"lone_high", `{"summary":"\ud800","model":"","created_at":0}`, ""},
		{"lone_low", `{"summary":"\udc00","model":"","created_at":0}`, ""},
		{"wrong_pair", `{"summary":"\ud800\u0041","model":"","created_at":0}`, ""},
		{"unknown_lone_surrogate_local_policy", `{"summary":"context","model":"","created_at":0,"extra":"\ud800"}`, ""},
		{"array_local_policy", `["context","",0]`, ""},
		{"trailing_json", `{"summary":"context","model":"","created_at":0}{}`, ""},
		{"bad_utf8", "{\"summary\":\"\xff\",\"model\":\"\",\"created_at\":0}", ""},
		{"unknown_bad_utf8_local_policy", "{\"summary\":\"context\",\"model\":\"\",\"created_at\":0,\"extra\":\"\xff\"}", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value := syntheticCapsule(t, []byte(c.plain))
			got, recognized, err := Open(value)
			if !recognized {
				t.Fatal("dedicated prefix was not recognized")
			}
			if c.want == "" {
				if err != errCapsule || got != "" {
					t.Fatal("invalid synthetic capsule accepted")
				}
			} else if err != nil || got != c.want {
				t.Fatal("valid synthetic capsule rejected or summary changed")
			}
		})
	}
}

func TestEnvelopeBoundaries(t *testing.T) {
	valid := syntheticCapsule(t, []byte(`{"summary":"context","model":"","created_at":0}`))
	sealed, err := base64.RawURLEncoding.DecodeString(valid[len(prefix):])
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	for name, value := range map[string]string{
		"empty":              prefix,
		"short":              prefix + base64.RawURLEncoding.EncodeToString(make([]byte, 27)),
		"bad_tag":            prefix + base64.RawURLEncoding.EncodeToString(sealed),
		"padding":            valid + "=",
		"newline":            valid + "\n",
		"invalid_alphabet":   prefix + "+/",
		"short_decoded_body": prefix + "AB",
		// This is a rejection control. Only source inspection establishes that
		// the budget check precedes decode/allocation; this is not a memory test.
		"over_budget_rejected": prefix + strings.Repeat("A", maxBodyBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			got, recognized, err := Open(value)
			if !recognized || err != errCapsule || got != "" {
				t.Fatal("invalid synthetic envelope accepted")
			}
		})
	}
	for _, value := range []string{"", "gAAAAABq-synthetic-native", "cpa-ag-compact-v1:synthetic", "AI-TOOLBOX-COMPACT-V1:synthetic"} {
		got, recognized, err := Open(value)
		if recognized || err != nil || got != "" {
			t.Fatal("foreign content was claimed")
		}
	}
}

func TestNoncanonicalTrailingBits(t *testing.T) {
	plain := []byte(`{"summary":"context","model":"","created_at":0}`)
	// Ensure the encoding has unused trailing bits without changing JSON data.
	if (12+len(plain)+16)%3 == 0 {
		plain = append(plain, ' ')
	}
	valid := syntheticCapsule(t, plain)
	body := valid[len(prefix):]
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, body[len(body)-1])
	if i < 0 || i&1 != 0 {
		t.Fatal("synthetic fixture does not have zero unused bits")
	}
	changed := body[:len(body)-1] + string(alphabet[i|1])
	canonical, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	lax, err := base64.RawURLEncoding.DecodeString(changed)
	if err != nil || !bytes.Equal(lax, canonical) {
		t.Fatal("fixture must decode to the same authenticated bytes with a lax decoder")
	}
	if got, recognized, err := Open(valid); err != nil || !recognized || got != "context" {
		t.Fatal("canonical authenticated control must succeed")
	}
	if got, recognized, err := Open(prefix + changed); err != errCapsule || !recognized || got != "" {
		t.Fatal("noncanonical encoding of valid authenticated bytes accepted")
	}
}
