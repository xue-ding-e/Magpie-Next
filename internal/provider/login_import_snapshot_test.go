package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The shape matches AI Toolbox's exported auth.json. Its refresh token can
// already have rotated while the access token remains valid at ChatGPT.
func importedSnapshot(t *testing.T) string {
	t.Helper()
	id := fakeJWT(map[string]any{"email": "snapshot@example.invalid",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "snapshot-seat", "chatgpt_plan_type": "plus"}})
	b, err := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil,
		"tokens":       map[string]string{"id_token": id, "access_token": "snapshot-access", "refresh_token": "snapshot-refresh-used", "account_id": "snapshot-seat"},
		"last_refresh": "2026-01-01T00:00:00Z", "future": json.RawMessage(`{"number":9007199254740993,"exponent":1e999}`)})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func snapshotServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	oldBase, oldToken := CodexBase, codexTokenURL
	CodexBase, codexTokenURL = srv.URL+"/codex", srv.URL+"/token"
	t.Cleanup(func() { CodexBase, codexTokenURL = oldBase, oldToken; srv.Close() })
	return srv
}

func TestImportCodexSnapshotKeepsUsableAccess(t *testing.T) {
	claudeHome(t)
	var refreshes atomic.Int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshes.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_reused"}}`))
			return
		}
		if r.URL.Path != "/wham/usage" || r.Header.Get("Authorization") != "Bearer snapshot-access" || r.Header.Get("chatgpt-account-id") != "snapshot-seat" {
			t.Errorf("unexpected verification request: %s", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"plus"}`))
	})
	// Keep this fixture readable by the old float64 parser too, so reverting
	// the fix fails specifically on the unnecessary refresh, before any
	// separate extreme-exponent preservation check.
	input := strings.ReplaceAll(importedSnapshot(t), "1e999", "1e99")
	rows, err := ImportLogins(context.Background(), "codex", []string{input})
	if err != nil || len(rows) != 1 || rows[0].Status != "added" {
		t.Fatalf("usable snapshot rejected: %+v, %v", rows, err)
	}
	if refreshes.Load() != 0 {
		t.Fatal("import consumed the source tool's refresh token")
	}
	preview, revision, err := CodexAuthJSON(rows[0].User, "", false)
	if err != nil || strings.Contains(string(preview), "snapshot-access") {
		t.Fatalf("preview failed or exposed credentials: %v", err)
	}
	auth, _, err := CodexAuthJSON(rows[0].User, revision, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{"snapshot-access", "snapshot-refresh-used", "2026-01-01T00:00:00Z", "9007199254740993", "1e99"} {
		if !strings.Contains(string(auth), preserved) {
			t.Errorf("import/export dropped snapshot field %q", preserved)
		}
	}
}

func TestImportCodexSnapshotRefreshesOnlyUnauthorizedAccess(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			claudeHome(t)
			var refreshes atomic.Int32
			snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					refreshes.Add(1)
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["refresh_reason"] != codexRefreshRecovery {
						t.Error("401 recovery did not use Codex's unauthorized refresh reason")
					}
					_, _ = w.Write([]byte(`{"access_token":"replacement-access","refresh_token":"replacement-refresh"}`))
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`not JSON`))
			})
			rows, err := ImportLogins(context.Background(), "codex", []string{importedSnapshot(t)})
			if err != nil || len(rows) != 1 {
				t.Fatalf("unexpected import result: %+v, %v", rows, err)
			}
			if status != http.StatusUnauthorized {
				if rows[0].Status != "failed" || refreshes.Load() != 0 {
					t.Fatalf("non-authentication failure rotated tokens: %+v, refreshes=%d", rows, refreshes.Load())
				}
				return
			}
			if rows[0].Status != "added" || refreshes.Load() != 1 {
				t.Fatalf("unauthorized access not refreshed once: %+v, refreshes=%d", rows, refreshes.Load())
			}
			_, revision, err := CodexAuthJSON(rows[0].User, "", false)
			if err != nil {
				t.Fatal(err)
			}
			auth, _, err := CodexAuthJSON(rows[0].User, revision, true)
			if err != nil || !strings.Contains(string(auth), "replacement-refresh") || !strings.Contains(string(auth), "9007199254740993") || !strings.Contains(string(auth), "1e999") {
				t.Fatalf("rotation or unknown metadata lost: %v", err)
			}
		})
	}
}

func TestImportCodexSnapshotPreservesSelectedWorkspace(t *testing.T) {
	claudeHome(t)
	var verified atomic.Int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wham/usage" {
			t.Error("unexpected refresh of a usable snapshot")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		verified.Add(1)
		if r.Header.Get("chatgpt-account-id") != "selected-seat" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"team"}`))
	})
	id := fakeJWT(map[string]any{"email": "workspace@example.invalid",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "jwt-default-seat", "chatgpt_plan_type": "team"}})
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{
		"id_token": id, "access_token": "usable-access", "refresh_token": "not-to-be-rotated", "account_id": "selected-seat"}})
	rows, err := ImportLogins(context.Background(), "codex", []string{string(raw)})
	if err != nil || len(rows) != 1 || rows[0].Status != "added" || verified.Load() != 1 {
		t.Fatalf("selected workspace was not verified/imported: %+v, %v", rows, err)
	}
	_, revision, err := CodexAuthJSON(rows[0].User, "", false)
	if err != nil {
		t.Fatal(err)
	}
	content, _, err := CodexAuthJSON(rows[0].User, revision, true)
	var auth codexAuth
	if err != nil || json.Unmarshal(content, &auth) != nil || auth.Tokens.AccountID != "selected-seat" {
		t.Fatal("export silently switched the selected workspace", err)
	}
}

func TestImportCodexSnapshotInvalidIdentityNeverRefreshes(t *testing.T) {
	for _, id := range []string{"", "invalid-id-token"} {
		t.Run(id, func(t *testing.T) {
			claudeHome(t)
			var requests atomic.Int32
			snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte(`{"plan_type":"plus"}`))
			})
			raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{
				"id_token": id, "access_token": "usable-access", "refresh_token": "must-not-be-consumed", "account_id": "selected-seat"}})
			rows, err := ImportLogins(context.Background(), "codex", []string{string(raw)})
			if err != nil || len(rows) != 1 || rows[0].Status != "failed" || !strings.Contains(rows[0].Error, "account identity") || requests.Load() != 0 {
				t.Fatalf("incomplete identity spent a token instead of rejecting the snapshot: %+v, %v; requests=%d", rows, err, requests.Load())
			}
		})
	}
}

func TestImportCodexSnapshotRefusedRefreshNeedsNewSnapshot(t *testing.T) {
	claudeHome(t)
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_reused"}}`))
	})
	rows, err := ImportLogins(context.Background(), "codex", []string{importedSnapshot(t)})
	if err != nil || len(rows) != 1 || rows[0].Status != "failed" || !strings.Contains(rows[0].Error, "export a fresh auth.json") {
		t.Fatalf("unusable snapshot not explained: %+v, %v", rows, err)
	}
	if strings.Contains(rows[0].Error, "snapshot-refresh-used") || strings.Contains(rows[0].Error, "snapshot-access") {
		t.Fatal("credentials appeared in an import error")
	}
}

func TestImportCodexSnapshotRejectsMixedAPIKeyAndTrailingJSON(t *testing.T) {
	if _, err := parseLoginImport("codex", importedSnapshot(t)+` {}`); err == nil {
		t.Fatal("accepted a second JSON document")
	}
	entries, err := parseLoginImport("codex", `{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-key","tokens":{"refresh_token":"synthetic-refresh"}}`)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].err, "API key") {
		t.Fatalf("mixed API key treated as subscription: %+v, %v", entries, err)
	}
}

// The same non-rotating check applies to every upstream-supported ChatGPT
// export, including codexbar's explicit workspace selection.
func TestImportCodexSnapshotOtherFormats(t *testing.T) {
	var auth codexAuth
	if err := json.Unmarshal([]byte(importedSnapshot(t)), &auth); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"cliproxyapi", "sub2api", "codexbar"} {
		t.Run(format, func(t *testing.T) {
			claudeHome(t)
			var verified, refreshed atomic.Int32
			snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					refreshed.Add(1)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				verified.Add(1)
				if r.URL.Path != "/wham/usage" || r.Header.Get("Authorization") != "Bearer snapshot-access" || r.Header.Get("chatgpt-account-id") != "snapshot-seat" {
					t.Error("the exported token and workspace did not reach verification")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_, _ = w.Write([]byte(`{"plan_type":"plus"}`))
			})
			var source any
			switch format {
			case "cliproxyapi":
				source = map[string]any{"type": "codex", "email": "snapshot@example.invalid", "id_token": auth.Tokens.IDToken,
					"access_token": auth.Tokens.AccessToken, "refresh_token": auth.Tokens.RefreshToken, "account_id": auth.Tokens.AccountID}
			case "sub2api":
				source = map[string]any{"accounts": []any{map[string]any{"platform": "openai", "type": "oauth", "credentials": map[string]any{
					"email": "snapshot@example.invalid", "id_token": auth.Tokens.IDToken, "access_token": auth.Tokens.AccessToken,
					"refresh_token": auth.Tokens.RefreshToken, "chatgpt_account_id": auth.Tokens.AccountID}}}}
			case "codexbar":
				source = map[string]any{"version": 1, "providers": []any{map[string]any{"kind": "openai_oauth", "accounts": []any{map[string]any{
					"kind": "oauth_tokens", "email": "snapshot@example.invalid", "idToken": auth.Tokens.IDToken,
					"accessToken": auth.Tokens.AccessToken, "refreshToken": auth.Tokens.RefreshToken, "openAIAccountId": auth.Tokens.AccountID}}}}}
			}
			b, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := ImportLogins(context.Background(), "codex", []string{string(b)})
			if err != nil || len(rows) != 1 || rows[0].Status != "added" || rows[0].User != "snapshot@example.invalid" || verified.Load() != 1 || refreshed.Load() != 0 {
				t.Fatalf("usable %s snapshot was not kept without refresh: %+v, %v; verified=%d, refreshed=%d", format, rows, err, verified.Load(), refreshed.Load())
			}
		})
	}
}

func TestImportCodexSnapshotAccessOnlyPreservesFormat(t *testing.T) {
	claudeHome(t)
	var requests atomic.Int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	input := strings.ReplaceAll(importedSnapshot(t), "snapshot-refresh-used", "")
	rows, err := ImportCodexAccessOnly(context.Background(), []string{input})
	if err != nil || len(rows) != 1 || rows[0].Status != "added" || requests.Load() != 0 {
		t.Fatalf("access-only snapshot was rejected or contacted ChatGPT: %+v, %v", rows, err)
	}
	_, revision, err := CodexAuthJSON(rows[0].User, "", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := CodexAuthJSON(rows[0].User, revision, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshot-access", "snapshot-seat", "2026-01-01T00:00:00Z", "9007199254740993", "1e999"} {
		if !strings.Contains(string(b), field) {
			t.Errorf("access-only snapshot lost %q", field)
		}
	}
	for _, l := range Logins("codex") {
		if l.User == rows[0].User && (!l.AccessOnly || l.Active) {
			t.Fatal("access-only account lost its mode or signed the agent in")
		}
	}
	rows, err = ImportCodexAccessOnly(context.Background(), []string{input})
	if err != nil || len(rows) != 1 || rows[0].Status != "exists" || requests.Load() != 0 {
		t.Fatalf("access-only duplicate changed upstream import semantics: %+v, %v", rows, err)
	}
}

func TestImportCodexSnapshotMissingAccessRefreshes(t *testing.T) {
	claudeHome(t)
	var refreshes atomic.Int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Error("a snapshot with no access token was sent for verification")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		refreshes.Add(1)
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["refresh_reason"] != codexRefreshForced || body["refresh_token"] != "snapshot-refresh-used" {
			t.Error("explicit import did not use Codex's forced refresh request")
		}
		_, _ = w.Write([]byte(`{"access_token":"replacement-access","refresh_token":"replacement-refresh"}`))
	})
	input := strings.ReplaceAll(importedSnapshot(t), "snapshot-access", "")
	rows, err := ImportLogins(context.Background(), "codex", []string{input})
	if err != nil || len(rows) != 1 || rows[0].Status != "added" || refreshes.Load() != 1 {
		t.Fatalf("missing access token was not refreshed once: %+v, %v", rows, err)
	}
	_, revision, err := CodexAuthJSON(rows[0].User, "", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := CodexAuthJSON(rows[0].User, revision, true)
	if err != nil || !strings.Contains(string(b), "replacement-refresh") || !strings.Contains(string(b), "9007199254740993") || !strings.Contains(string(b), "1e999") {
		t.Fatal("refresh lost the new tokens or unknown precise metadata", err)
	}
}

func TestImportCodexSnapshotCancelledVerificationNeverRefreshes(t *testing.T) {
	claudeHome(t)
	var requests atomic.Int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := ImportLogins(ctx, "codex", []string{importedSnapshot(t)})
	if err != nil || len(rows) != 1 || rows[0].Status != "failed" || requests.Load() != 0 {
		t.Fatalf("cancelled verification sent a request or kept the account: %+v, %v", rows, err)
	}
}

func TestImportSnapshotCswapNumberFormats(t *testing.T) {
	for _, version := range []string{"1", "1.0", "1e0"} {
		input := `{"swapVersion":"0.29","version":` + version + `,"accounts":[{"email":"numbers@example.invalid","credentials":{"claudeAiOauth":{"accessToken":"synthetic-access","refreshToken":"synthetic-refresh","expiresAt":1.9e12}},"config":{},"alias":"work"}]}`
		entries, err := parseLoginImport("claude", input)
		if err != nil || len(entries) != 1 || entries[0].err != "" || entries[0].expiresAt != 1900000000000 || entries[0].alias != "work" {
			t.Fatalf("precise Codex decoding changed Claude-swap's numeric fields: %v, %v", entries, err)
		}
	}
}
