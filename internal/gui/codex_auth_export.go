package gui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/steady"
)

// Set only after webGuard verifies its run-key cookie; a network header
// cannot impersonate this, or ownWindow's native transport context.
type authenticatedWebKey struct{}

// These routes stay under Handler's normal web authentication and origin
// checks. Secrets never appear in a URL, a status message or a log.
func codexAuthRoutes(mux *http.ServeMux, w Windows) {
	mux.HandleFunc("POST /api/login/auth-json", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")
		var in struct {
			User     string
			Revision string
			Action   string
		}
		bad := func() { fail(rw, errors.New("Could not prepare this saved Codex sign-in")) }
		// A simple cross-site form must never reveal or export a sign-in.
		// Reject other ports on the same host too (Sec-Fetch-Site alone
		// calls those same-site). Native requests without Origin still use
		// JSON and Handler's frame guard, as the rest of the app does.
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		origin := r.Header.Get("Origin")
		if media != "application/json" || !codexAuthOrigin(r, origin) {
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}
		if json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in) != nil {
			bad()
			return
		}
		switch in.Action {
		case "preview", "reveal", "export":
		default:
			bad()
			return
		}
		b, revision, err := provider.CodexAuthJSON(in.User, in.Revision, in.Action != "preview")
		if err != nil || r.Context().Err() != nil {
			bad()
			return
		}
		if in.Action != "export" {
			writeJSON(rw, map[string]string{"content": string(b), "revision": revision})
			return
		}
		if isWeb(w) {
			// A browser downloads to its own computer, never the web server's
			// Downloads directory. The page receives these bytes only on Export.
			writeJSON(rw, map[string]string{"content": string(b), "revision": revision})
			return
		}
		path, err := saveCodexAuthExport(r.Context(), downloads(), b)
		if err != nil {
			bad()
			return
		}
		writeJSON(rw, map[string]string{"path": tilde(path)})
	})
}

func codexAuthOrigin(r *http.Request, origin string) bool {
	if apiFramed(r) {
		return false
	}
	native, _ := r.Context().Value(windowSchemeKey{}).(string)
	web, _ := r.Context().Value(authenticatedWebKey{}).(bool)
	if native == "" && !web {
		// In particular, the development TCP listener must not expose this
		// new sensitive API without the production web authentication guard.
		return false
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Host != r.Host || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if native != "" {
		return u.Scheme == native
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// A unique private directory prevents overwriting auth.json or following a
// pre-existing symlink. The file is flushed and renamed atomically; a failed
// write removes its temporary file and the empty export directory.
func saveCodexAuthExport(ctx context.Context, dir string, b []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(dir, "magpie-codex-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "auth.json")
	if err := ctx.Err(); err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	if err := steady.WriteFile(path, b, 0o600); err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	if err := ctx.Err(); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return "", removeErr
		}
		_ = os.Remove(dir)
		return "", err
	}
	return path, nil
}
