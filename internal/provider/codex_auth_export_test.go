package provider

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// These are invented credentials; the test never discovers a live account.
const exportSyntheticAuth = `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","id_token":"synthetic-id","account_id":"synthetic-account"},"last_refresh":"2026-01-01T00:00:00Z","future-private-key":{"nested":["synthetic-future",9007199254740993,1e999,true,null]}}`

func exportSyntheticLogin() savedLogin {
	return savedLogin{ID: "saved-synthetic", Agent: "codex", User: "synthetic@example.invalid", Auth: json.RawMessage(exportSyntheticAuth)}
}

func TestSavedCodexAuthJSONPreservesOriginal(t *testing.T) {
	l := exportSyntheticLogin()
	_, revision, err := savedCodexAuthJSON([]savedLogin{l}, l.User, "", false)
	if err != nil {
		t.Fatal(err)
	}
	b, id, err := savedCodexAuthJSON([]savedLogin{l}, l.User, revision, true)
	if err != nil || id != revision {
		t.Fatalf("export identity: %q %v", id, err)
	}
	var original, exported bytes.Buffer
	if json.Compact(&original, l.Auth) != nil || json.Compact(&exported, b) != nil || !bytes.Equal(original.Bytes(), exported.Bytes()) {
		t.Fatal("export changed the original JSON, unknown fields or number precision")
	}
	if string(l.Auth) != exportSyntheticAuth {
		t.Fatal("export mutated saved account")
	}
}

func TestSavedCodexAuthJSONMasksUnknownNestedFields(t *testing.T) {
	l := exportSyntheticLogin()
	b, id, err := savedCodexAuthJSON([]savedLogin{l}, l.User, "", false)
	if err != nil || id == "" || id == l.ID || !json.Valid(b) {
		t.Fatal("masked preview failed")
	}
	for _, secret := range []string{"synthetic", "future-private-key", "nested", "9007199254740993", "1e999", "2026-01-01", "chatgpt"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("preview leaked %q", secret)
		}
	}
	if !strings.Contains(string(b), `"access_token": "[hidden]"`) {
		t.Fatal("preview lost the auth.json shape")
	}
}

func TestSavedCodexAuthJSONRejectsWrongModeAndIdentity(t *testing.T) {
	good := exportSyntheticLogin()
	for _, tt := range []struct {
		name  string
		login savedLogin
		id    string
	}{
		{"no explicit identity", good, ""},
		{"replaced identity", good, "other"},
		{"API key", savedLogin{ID: good.ID, Agent: "codex", User: good.User, Auth: json.RawMessage(`{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-key","tokens":{"access_token":"synthetic-access"}}`)}, good.ID},
		{"malformed", savedLogin{ID: good.ID, Agent: "codex", User: good.User, Auth: json.RawMessage(`{"synthetic-secret"`)}, good.ID},
		{"wrong provider", savedLogin{ID: good.ID, Agent: "claude", User: good.User, Auth: good.Auth}, good.ID},
		{"hidden", savedLogin{ID: good.ID, Agent: "codex", User: good.User, Auth: good.Auth, Hidden: "removed"}, good.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, _, err := savedCodexAuthJSON([]savedLogin{tt.login}, good.User, tt.id, true)
			if err != errCodexAuthExport || len(b) != 0 {
				t.Fatal("unsafe export was not rejected with a generic error")
			}
			if tt.name != "no explicit identity" && tt.name != "replaced identity" {
				if b, _, err := savedCodexAuthJSON([]savedLogin{tt.login}, good.User, "", false); err != errCodexAuthExport || len(b) != 0 {
					t.Fatal("invalid saved login allowed a preview")
				}
			}
		})
	}
	if b, _, err := savedCodexAuthJSON([]savedLogin{good, good}, good.User, "", false); err == nil || len(b) != 0 {
		t.Fatal("ambiguous account exported")
	}
}

// Deleting/re-adding a name can retain loginID, and refreshing an existing
// entry retains its persistent ID too. Neither may reuse a preview's approval.
func TestSavedCodexAuthJSONRejectsChangedSnapshot(t *testing.T) {
	for _, persistentID := range []string{"", "saved-synthetic"} {
		a := exportSyntheticLogin()
		a.ID = persistentID
		_, revision, err := savedCodexAuthJSON([]savedLogin{a}, a.User, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range []struct{ name, old, replacement string }{
			{"workspace", "synthetic-account", "synthetic-other-workspace"},
			{"refresh", "synthetic-access", "synthetic-refreshed-access"},
		} {
			t.Run(persistentID+"/"+change.name, func(t *testing.T) {
				b := a
				b.Auth = json.RawMessage(strings.Replace(string(a.Auth), change.old, change.replacement, 1))
				if a.User != b.User || loginID(a) != loginID(b) {
					t.Fatal("fixture must retain the same name and login ID")
				}
				if raw, _, err := savedCodexAuthJSON([]savedLogin{b}, a.User, revision, true); err != errCodexAuthExport || len(raw) != 0 {
					t.Fatal("stale preview exported a replacement or refreshed credential")
				}
				_, fresh, err := savedCodexAuthJSON([]savedLogin{b}, b.User, "", false)
				if err != nil || fresh == revision {
					t.Fatal("replacement did not get a new revision")
				}
				raw, _, err := savedCodexAuthJSON([]savedLogin{b}, b.User, fresh, true)
				if err != nil || !strings.Contains(string(raw), change.replacement) {
					t.Fatal("new preview did not approve the new snapshot")
				}
			})
		}
	}
}
