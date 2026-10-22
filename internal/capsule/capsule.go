// Package capsule is an unintegrated, in-memory compatibility experiment.
// It does not read histories, modify messages, or decide migration policy.
package capsule

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const prefix = "ai-toolbox-compact-v1:"

// maxBodyBytes bounds the encoded body before allocation. This is a local
// resource policy, not an advertised limit of the originating Rust writer.
const maxBodyBytes = 16 << 20

var errCapsule = errors.New("dedicated summary capsule is unreadable; preserve the original")

// Open recognizes only the dedicated prefix. A nonmatching value returns
// ("", false, nil), including native encrypted reasoning. A matching invalid
// value returns ("", true, errCapsule). Neither case authorizes dropping or
// rewriting an item. Errors deliberately contain no input or decrypted data.
//
// On success the summary is returned exactly, including surrounding whitespace.
// The originating Rust gateway trims its returned summary; this experiment
// checks nonemptiness after trimming but does not silently discard text.
func Open(value string) (summary string, recognized bool, err error) {
	if !strings.HasPrefix(value, prefix) {
		return "", false, nil
	}
	body := value[len(prefix):]
	if len(body) > maxBodyBytes {
		return "", true, errCapsule
	}
	// Go's base64 decoder tolerates CR/LF even in strict mode. The writer's
	// URL_SAFE_NO_PAD representation contains only these ASCII characters.
	for i := 0; i < len(body); i++ {
		c := body[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", true, errCapsule
		}
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil || len(sealed) < 12+16 {
		return "", true, errCapsule
	}
	// Public protocol key material, not a credential or confidentiality claim.
	key := sha256.Sum256([]byte("ai-toolbox-remote-compaction"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", true, errCapsule
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", true, errCapsule
	}
	plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
	if err != nil {
		return "", true, errCapsule
	}
	summary, err = readSummary(plain)
	if err != nil {
		return "", true, errCapsule
	}
	return summary, true, nil
}

func readSummary(plain []byte) (string, error) {
	if !utf8.Valid(plain) || !json.Valid(plain) || !scalarEscapes(plain) {
		return "", errCapsule
	}
	d := json.NewDecoder(bytes.NewReader(plain))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return "", errCapsule
	}
	var summary string
	seen := make(map[string]bool, 3)
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return "", errCapsule
		}
		name, ok := t.(string)
		if !ok {
			return "", errCapsule
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return "", errCapsule
		}
		switch name {
		case "summary", "model", "created_at":
			if seen[name] {
				return "", errCapsule
			}
			seen[name] = true
		default:
			// Match serde's default unknown-field policy. In particular,
			// case variants are unknown, not aliases of known fields.
			continue
		}
		if name == "created_at" {
			var stamp int64
			// serde_json represents -0 as f64; its i64 visitor rejects it.
			// Go's int64 unmarshaller alone would accept that spelling.
			if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("-0")) || json.Unmarshal(raw, &stamp) != nil {
				return "", errCapsule
			}
		} else {
			var text string
			if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
				return "", errCapsule
			}
			if name == "summary" {
				summary = text
			}
		}
	}
	if _, err := d.Token(); err != nil {
		return "", errCapsule
	}
	if _, err := d.Token(); err != io.EOF {
		return "", errCapsule
	}
	if !seen["summary"] || !seen["model"] || !seen["created_at"] || strings.TrimSpace(summary) == "" {
		return "", errCapsule
	}
	return summary, nil
}

// scalarEscapes runs only after JSON syntax and raw UTF-8 are validated. Go's
// encoding/json otherwise replaces lone surrogate escapes with U+FFFD. Reject
// them rather than silently changing text, including in ignored fields. That
// ignored-field restriction is deliberately stricter than serde's IgnoredAny.
func scalarEscapes(b []byte) bool {
	inString := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || b[i] != '\\' {
			continue
		}
		i++
		if b[i] != 'u' {
			continue
		}
		u := hex4(b[i+1 : i+5])
		i += 4
		if u >= 0xdc00 && u <= 0xdfff {
			return false
		}
		if u >= 0xd800 && u <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			v := hex4(b[i+3 : i+7])
			if v < 0xdc00 || v > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func hex4(b []byte) uint16 {
	var n uint16
	for _, c := range b {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			n += uint16(c-'a') + 10
		default:
			n += uint16(c-'A') + 10
		}
	}
	return n
}
