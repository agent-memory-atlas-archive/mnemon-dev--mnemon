package opencode_test

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// JavaScript runtime tests belong to the explicit integration tier.
func TestPluginRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("OpenCode plugin runtime tests require Node.js 22.3 or newer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--experimental-test-module-mocks", "--test", "plugin.test.mjs", "timeout.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("OpenCode plugin runtime: %v\n%s", err, output)
	}
}
