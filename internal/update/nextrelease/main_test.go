package nextrelease

import (
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// Match the parent update package's isolation. Metadata tests use synthetic
// loopback servers only, and must not inherit live settings or reach the network.
func TestMain(m *testing.M) {
	testenv.Offline()
	testenv.Main(m)
}
