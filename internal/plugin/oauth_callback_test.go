package plugin

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestHostOAuthCallbackKeepsAccessOnly exercises the production JS with fake
// auth storage and no vendor requests, including one-shot callback sessions.
func TestHostOAuthCallbackKeepsAccessOnly(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/oauth_callback.cjs")
	cmd.Stdin = bytes.NewReader(hostJS)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("OAuth callback regression: %v\n%s", err, out)
	}
}
