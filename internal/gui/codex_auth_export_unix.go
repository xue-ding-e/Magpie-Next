//go:build !windows

package gui

import (
	"os"

	"github.com/yetone/magpie/internal/steady"
)

func makeCodexAuthExportDirectory(parent string) (string, func(), error) {
	dir, err := os.MkdirTemp(parent, "magpie-codex-")
	return dir, func() {}, err
}

func writeCodexAuthExport(path string, b []byte) error {
	return steady.WriteFile(path, b, 0o600)
}
