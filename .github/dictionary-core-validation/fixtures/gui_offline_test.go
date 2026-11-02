package gui

import "github.com/yetone/magpie/internal/testenv"

// The selected API fixtures are synthetic; even an accidental background
// default transport request must not reach a vendor.
func init() { testenv.Offline() }
