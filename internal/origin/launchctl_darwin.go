//go:build darwin

package origin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultLaunchctlRunner shells out to /bin/launchctl. It returns the combined
// stdout+stderr so callers can include it in error messages.
func DefaultLaunchctlRunner(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// `launchctl unload` of an already-unloaded job exits non-zero with a
		// "Could not find specified service" message — treat that as success.
		if strings.Contains(string(out), "Could not find") || strings.Contains(string(out), "not loaded") {
			return out, nil
		}
		return out, fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
