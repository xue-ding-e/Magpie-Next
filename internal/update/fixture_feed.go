package update

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureFeed keeps the inherited HTTP transport fixtures usable in Go test
// binaries, including tests of packages that import update. Ordinary go build
// binaries cannot activate it, and Feed/NotesFeed always report the fixed Next
// source even in tests. A valid testenv sandbox is required, and only HTTP
// loopback or .invalid hosts qualify. Their fake binaries can also be staged
// by tests on unsupported platforms.
func fixtureFeed(key string) string {
	if key != "MAGPIE_UPDATE_FEED" && key != "MAGPIE_NOTES_FEED" {
		return ""
	}
	raw := os.Getenv(key)
	if fixtureEndpoint(raw) {
		return raw
	}
	return ""
}

// fixtureEndpoint also bounds direct inherited transport fixtures. Keeping
// this gate on the shared reader prevents a fallback from selecting an
// upstream repository in an ordinary source or release build.
func fixtureEndpoint(raw string) bool {
	if !testing.Testing() || !fixtureSandbox() {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() || strings.HasSuffix(host, ".invalid") {
		return true
	}
	return false
}

// fixtureSandbox checks testenv's marker and home/config/cache roots. Tests
// may move an individual root into another t.TempDir, as testenv.SetHome does,
// but none may resolve outside the system temporary directory.
func fixtureSandbox() bool {
	home := os.Getenv("MAGPIE_TEST_SANDBOX")
	if !filepath.IsAbs(home) {
		return false
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return false
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return false
	}
	for _, path := range []string{home, os.Getenv("HOME"), os.Getenv("USERPROFILE"),
		os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_CACHE_HOME"),
		os.Getenv("XDG_DATA_HOME"), os.Getenv("XDG_STATE_HOME"),
		os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
		if !filepath.IsAbs(path) {
			return false
		}
		// A root need not exist yet. Resolve its nearest existing ancestor
		// so a junction or symlink cannot make a temporary path a live one.
		ancestor := filepath.Clean(path)
		for {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err == nil {
				rel, err := filepath.Rel(tmp, resolved)
				if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return false
				}
				break
			}
			if !os.IsNotExist(err) || filepath.Dir(ancestor) == ancestor {
				return false
			}
			ancestor = filepath.Dir(ancestor)
		}
	}
	return true
}
