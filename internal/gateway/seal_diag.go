package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
)

// diagSealRefusal writes what an upstream refused a Responses turn with, for
// a Codex sign-in, so a 400 whose body reaches Codex as "Bad Request" can be
// read afterwards. Temporary: set MAGPIE_SEAL_DIAG to an absolute file path
// to turn it on. Existing files must be regular and private on Unix; Windows
// inherits the chosen directory's ACL, as other local private files do.
// It records the status, the content type, the body as it came (cut short),
// and the shape of the request: which input items carry a seal and of what
// type, without any of the sealed content itself.
var diagSeal = struct {
	sync.Mutex
	n int
}{}

// Routed accounts also need their raw error before APIError reduces an
// unrecognised error envelope to the HTTP status. Preserve the complete reply
// for the caller; only the bounded prefix is retained in the diagnostic.
func diagRoutedCodex(res *http.Response, body []byte) {
	if appdir.Getenv("MAGPIE_SEAL_DIAG") == "" || res.StatusCode < 400 {
		return
	}
	head, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body = readCloser{io.MultiReader(bytes.NewReader(head), res.Body), res.Body}
	diagSealRefusal(res.StatusCode, res.Header, head, body)
}

func diagSealRefusal(status int, h http.Header, msg, body []byte) {
	path := appdir.Getenv("MAGPIE_SEAL_DIAG")
	if path == "" {
		return
	}
	diagSeal.Lock()
	defer diagSeal.Unlock()
	diagSeal.n++
	rec := map[string]any{
		"at":      time.Now().Format(time.RFC3339Nano),
		"n":       diagSeal.n,
		"status":  status,
		"ctype":   h.Get("Content-Type"),
		"len":     len(msg),
		"body":    strings.ToValidUTF8(string(msg), "?"),
		"matched": foreignReasoning.Match(msg),
		"sealed":  sealShape(body),
	}
	appendSealDiagnostic(path, rec)
}

// diagTurnRefusal is diagSealRefusal for whatever a turn ended with, called
// where a native Codex call is recorded: a turn, a subagent's spawn, and
// Codex's own account probes alike. Routed providers use their ordinary record.
func diagTurnRefusal(kind, endpoint string, status int, refusal, body []byte) {
	path := appdir.Getenv("MAGPIE_SEAL_DIAG")
	if path == "" || status < 400 {
		return
	}
	diagSeal.Lock()
	defer diagSeal.Unlock()
	diagSeal.n++
	rec := map[string]any{
		"at":       time.Now().Format(time.RFC3339Nano),
		"n":        diagSeal.n,
		"kind":     kind,
		"endpoint": endpoint,
		"status":   status,
		"len":      len(refusal),
		"body":     strings.ToValidUTF8(string(refusal), "?"),
		"matched":  foreignReasoning.Match(refusal),
		"sealed":   sealShape(body),
	}
	appendSealDiagnostic(path, rec)
}

// Diagnostics never truncate an existing file or change its permissions.
// Refuse links and shared files rather than append raw upstream errors to
// them. Recheck the opened handle against the path before writing.
func appendSealDiagnostic(path string, rec map[string]any) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	before, err := os.Lstat(path)
	var f *os.File
	if os.IsNotExist(err) {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	} else if err == nil && privateSealDiagnostic(before) {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	} else {
		return
	}
	if err != nil {
		return
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !privateSealDiagnostic(opened) || (before != nil && !os.SameFile(before, opened)) {
		return
	}
	after, err := os.Lstat(path)
	if err != nil || !privateSealDiagnostic(after) || !os.SameFile(opened, after) {
		return
	}
	f.Write(append(b, '\n'))
}

func privateSealDiagnostic(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (runtime.GOOS == "windows" || (info.Mode().Perm()&0077 == 0 && info.Mode().Perm()&0200 != 0))
}

// sealShape says, for each input item type, how many carry a seal and how
// many do not — never the seal itself.
func sealShape(body []byte) map[string]map[string]int {
	var q struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &q) != nil {
		return nil
	}
	out := map[string]map[string]int{}
	for _, it := range q.Input {
		var typ string
		json.Unmarshal(it["type"], &typ)
		if typ == "" {
			typ = "(none)"
		}
		if out[typ] == nil {
			out[typ] = map[string]int{"sealed": 0, "plain": 0}
		}
		var enc string
		json.Unmarshal(it["encrypted_content"], &enc)
		if enc != "" {
			out[typ]["sealed"]++
		} else {
			out[typ]["plain"]++
		}
	}
	return out
}
