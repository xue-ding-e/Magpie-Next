package gateway

import "github.com/yetone/magpie/internal/testenv"

// Synthetic checks never use a real vendor; block accidental default HTTP
// transport requests as well as relying on the repository's isolated HOME.
func init() { testenv.Offline() }
