// Architectural guards: enforce the import discipline that the rest of
// the codebase relies on. CLI and TUI consume only cron's public
// interface; they must not name a concrete backend (crontab, launchd,
// systemd, etccron) directly. If they did, swapping or extending
// backends would require touching the UI layers — exactly the coupling
// the cron.Source interface exists to prevent.

package tests

import (
	"go/build"
	"strings"
	"testing"
)

func TestCLIAndTUIOnlyDependOnCronInterface(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"github.com/rednafi/eon/cron/crontab",
		"github.com/rednafi/eon/cron/launchd",
		"github.com/rednafi/eon/cron/systemd",
		"github.com/rednafi/eon/cron/etccron",
	}
	pkgs := []string{
		"github.com/rednafi/eon/cli",
		"github.com/rednafi/eon/tui",
	}
	// Walk every supported GOOS so a forbidden import gated by
	// //go:build linux doesn't ship green on a darwin runner (and vice
	// versa). build.Default would only see the host's tags.
	for _, goos := range []string{"darwin", "linux"} {
		ctx := build.Default
		ctx.GOOS = goos
		for _, pkg := range pkgs {
			p, err := ctx.Import(pkg, "", 0)
			if err != nil {
				t.Fatalf("[%s] import %s: %v", goos, pkg, err)
			}
			for _, imp := range p.Imports {
				for _, banned := range forbidden {
					if strings.HasPrefix(imp, banned) {
						t.Errorf("[%s] %s imports %s — UI layers must consume cron via the Source/Mutator interface only", goos, pkg, imp)
					}
				}
			}
		}
	}
}
