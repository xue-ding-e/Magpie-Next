package gui

import (
	"net"
	"os"
	"testing"

	"github.com/yetone/magpie/internal/fx"
	"github.com/yetone/magpie/internal/testenv"
)

// TestMain keeps the rate off the network: a state read with no rate cached
// asks for one behind it (#541), which would otherwise reach the real
// endpoint and land in whatever test runs next. And the tests run in a
// home of their own, never the user's real files. Vendor requests must be
// served on loopback, as in the provider and gateway packages' tests.
func TestMain(m *testing.M) {
	// started by TestGatewayFixQuitsOnlyAMagpie as an older magpie
	if addr := os.Getenv("MAGPIE_TEST_FAKE_GATEWAY"); addr != "" {
		fakeOlderGateway(addr)
		return
	}
	notesURL := "http://notes.invalid/"
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		addr := l.Addr().String()
		l.Close() // refused at once from here on
		fx.SetRateURL("http://" + addr + "/")
		notesURL = "http://" + addr + "/"
	}
	os.Exit(testenv.RunIn(m, func(string) {
		testenv.Offline()
		// Set this after isolation clears inherited agent variables, so
		// no GUI test asks the site for release notes.
		os.Setenv("MAGPIE_NOTES_FEED", notesURL)
	}))
}
