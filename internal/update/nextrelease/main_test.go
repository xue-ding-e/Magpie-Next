package nextrelease

import (
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// Match the parent update package's isolation even though these tests use only
// synthetic bytes and callbacks: future tests must not inherit live settings.
func TestMain(m *testing.M) { testenv.Main(m) }
