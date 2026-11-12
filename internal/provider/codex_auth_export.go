package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/steady"
)

var errCodexAuthExport = errors.New("Could not prepare this saved Codex sign-in")

// CodexAuthJSON exports only an already remembered subscription account. It
// never discovers a live credential, refreshes a token, switches accounts or
// falls back to a stale last-good file after a failed read. This is a snapshot
// of magpie's saved sign-in, not a promise that its tokens are still valid.
func CodexAuthJSON(user, revision string, reveal bool) ([]byte, string, error) {
	if user == "" || settings.AccountDiscoveryOff() {
		return nil, "", errCodexAuthExport
	}
	savedTokenMu.Lock()
	defer savedTokenMu.Unlock()
	loginsMu.Lock()
	defer loginsMu.Unlock()
	b, err := steady.ReadFile(loginsPath())
	if err != nil {
		return nil, "", errCodexAuthExport
	}
	ls, err := parseLogins(b)
	if err != nil {
		return nil, "", errCodexAuthExport
	}
	return savedCodexAuthJSON(ls, user, revision, reveal)
}

func savedCodexAuthJSON(ls []savedLogin, user, revision string, reveal bool) ([]byte, string, error) {
	if reveal && revision == "" {
		return nil, "", errCodexAuthExport
	}
	var auth json.RawMessage
	var selectedRevision string
	for _, l := range ls {
		if l.Agent == "codex" && strings.EqualFold(l.User, user) && l.Hidden == "" {
			if auth != nil { // ambiguous identity must not export another account
				return nil, "", errCodexAuthExport
			}
			// A name-derived or persistent login ID is not a revision: the
			// same entry can be replaced or refreshed between preview and
			// reveal. Bind approval to the complete saved account snapshot,
			// including its original Auth and workspace metadata. Changes
			// require a new preview, rather than exporting a different sign-in.
			snapshot, err := json.Marshal(l)
			if err != nil {
				return nil, "", errCodexAuthExport
			}
			selectedRevision = fmt.Sprintf("snapshot-%x", sha256.Sum256(snapshot))
			if revision != "" && revision != selectedRevision {
				return nil, "", errCodexAuthExport
			}
			auth = l.Auth
		}
	}
	var a codexAuth
	if json.Unmarshal(auth, &a) != nil || a.AuthMode == "apikey" || a.Tokens.AccessToken == "" {
		return nil, "", errCodexAuthExport
	}
	// API keys belong to Keys, not these subscription accounts. Do not
	// manufacture OAuth credentials or a provider configuration from a key.
	var fields map[string]json.RawMessage
	if json.Unmarshal(auth, &fields) != nil || fields == nil {
		return nil, "", errCodexAuthExport
	}
	if key, ok := fields["OPENAI_API_KEY"]; ok && string(key) != "null" && string(key) != `""` {
		return nil, "", errCodexAuthExport
	}
	if !reveal {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(auth))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, "", errCodexAuthExport
		}
		// Unknown future fields and nested arrays are private too. Only the
		// shape is previewed; no credential or account identifier is returned.
		auth, _ = json.Marshal(maskCodexAuth(value))
	}
	var out bytes.Buffer
	if json.Indent(&out, auth, "", "  ") != nil {
		return nil, "", errCodexAuthExport
	}
	out.WriteByte('\n')
	return out.Bytes(), selectedRevision, nil
}

func maskCodexAuth(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for i, k := range keys {
			name := k
			switch k {
			case "auth_mode", "OPENAI_API_KEY", "tokens", "last_refresh", "id_token", "access_token", "refresh_token", "account_id":
			default:
				// Unknown map keys may themselves be account identifiers.
				name = fmt.Sprintf("[hidden field %d]", i+1)
			}
			out[name] = maskCodexAuth(x[k])
		}
		return out
	case []any:
		for i, value := range x {
			x[i] = maskCodexAuth(value)
		}
		return x
	default:
		return "[hidden]"
	}
}
