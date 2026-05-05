// Black-box CLI tests: build the actual eon binary and drive it via os/exec
// against a hermetic environment. Crontab access is shimmed via a tiny
// shell script in PATH so the suite never touches the real spool. No build
// tag — runs on every platform we ship for.

package tests

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce  sync.Once
	binaryPath string
	buildErr   error
)

// buildBinary compiles the eon command once for the test process. We keep
// it in os.TempDir() so a second `go test` run hits a warm `go build`
// cache. Returns the path to the binary or an error if the compile failed.
func buildBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "eon-blackbox-")
		if err != nil {
			buildErr = err
			return
		}
		binaryPath = filepath.Join(dir, "eon")
		// Walk up from the test pkg's working dir to the module root.
		moduleRoot, err := exec.Command("go", "env", "GOMOD").Output()
		if err != nil {
			buildErr = fmt.Errorf("go env GOMOD: %w", err)
			return
		}
		root := filepath.Dir(strings.TrimSpace(string(moduleRoot)))
		cmd := exec.Command("go", "build", "-o", binaryPath, ".")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %w: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build: %v", buildErr)
	}
	return binaryPath
}

// hermeticEnv returns env vars that isolate `eon` from the developer's
// real cron / launchctl / systemd state. We:
//   - point HOME at a tmpdir so launchd/systemd dirs are empty,
//   - prepend a fake-crontab dir to PATH so `crontab -l/-r/-` writes to
//     a state file we control,
//   - clear EON_RUN_REAL_CRON so any nested cron tests stay skipped.
func hermeticEnv(t *testing.T) (env []string, home, fakeBin string) {
	t.Helper()
	tmp := t.TempDir()
	home = filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	fakeBin = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fakebin: %v", err)
	}
	stateFile := filepath.Join(tmp, "crontab.spool")
	// Tiny shell shim. Implements -l/-r/- against $stateFile; everything
	// else is a no-op (mirrors how real crontab swallows unknown args).
	shim := fmt.Sprintf(`#!/bin/sh
state="%s"
case "$1" in
  -l)
    if [ -s "$state" ]; then cat "$state"; else echo "no crontab for tester"; exit 1; fi
    ;;
  -r) : > "$state" ;;
  -)  cat > "$state" ;;
  *)  : ;;
esac
`, stateFile)
	if err := os.WriteFile(filepath.Join(fakeBin, "crontab"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	// touch the state file so `-l` doesn't blow up before the user adds anything.
	if err := os.WriteFile(stateFile, nil, 0o600); err != nil {
		t.Fatalf("touch spool: %v", err)
	}
	env = append(os.Environ(),
		"HOME="+home,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"EON_RUN_REAL_CRON=",
	)
	return env, home, fakeBin
}

// runEon shells out to the test binary. Returns combined stdout/stderr,
// stdin-driven if non-nil, and the exit code (-1 for non-ExitError).
func runEon(t *testing.T, env []string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(buildBinary(t), args...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return out.String(), ee.ExitCode()
	}
	t.Fatalf("eon: %v\n%s", err, out.String())
	return out.String(), -1
}

func TestBlackboxHelpListsAllSubcommands(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)
	out, code := runEon(t, env, "", "--help")
	if code != 0 {
		t.Fatalf("help exit = %d:\n%s", code, out)
	}
	for _, want := range []string{"list", "show", "logs", "add", "edit", "delete"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help missing subcommand %q:\n%s", want, out)
		}
	}
}

func TestBlackboxListEmpty(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)
	out, code := runEon(t, env, "", "list")
	if code != 0 {
		t.Fatalf("list exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(no scheduled jobs)") {
		t.Errorf("expected empty-state message:\n%s", out)
	}
}

func TestBlackboxAddListEditDelete(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)

	// add
	out, code := runEon(t, env, "", "add", "--schedule", "@daily", "--command", "/bin/echo hi")
	if code != 0 {
		t.Fatalf("add exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "added crontab:") {
		t.Errorf("add output unexpected:\n%s", out)
	}

	// list -> JSON, find the new ID
	out, code = runEon(t, env, "", "list", "--json")
	if code != 0 {
		t.Fatalf("list --json exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"Schedule": "@daily"`) {
		t.Errorf("list missing scheduled job:\n%s", out)
	}

	// substring lookup with show
	out, code = runEon(t, env, "", "show", "/bin/echo")
	if code != 0 {
		t.Fatalf("show exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Command:") || !strings.Contains(out, "/bin/echo hi") {
		t.Errorf("show missing fields:\n%s", out)
	}

	// edit
	out, code = runEon(t, env, "", "edit", "/bin/echo", "--schedule", "@hourly")
	if code != 0 {
		t.Fatalf("edit exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "edited crontab:") {
		t.Errorf("edit output unexpected:\n%s", out)
	}

	// confirm edit landed
	out, _ = runEon(t, env, "", "list", "--json")
	if !strings.Contains(out, `"Schedule": "@hourly"`) {
		t.Errorf("edit didn't change schedule:\n%s", out)
	}

	// delete --yes
	out, code = runEon(t, env, "", "delete", "/bin/echo", "--yes")
	if code != 0 {
		t.Fatalf("delete exit = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "deleted crontab:") {
		t.Errorf("delete output unexpected:\n%s", out)
	}

	// final list is empty again
	out, _ = runEon(t, env, "", "list")
	if !strings.Contains(out, "(no scheduled jobs)") {
		t.Errorf("after delete list should be empty:\n%s", out)
	}
}

func TestBlackboxDeletePromptDeniesByDefault(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)
	if _, code := runEon(t, env, "", "add", "--schedule", "@daily", "--command", "/bin/keep"); code != 0 {
		t.Fatalf("setup add failed")
	}
	out, code := runEon(t, env, "n\n", "delete", "/bin/keep")
	if code == 0 {
		t.Errorf("denied delete should exit non-zero:\n%s", out)
	}
	listOut, _ := runEon(t, env, "", "list", "--json")
	if !strings.Contains(listOut, "/bin/keep") {
		t.Errorf("denied delete should keep the job; list:\n%s", listOut)
	}
}

func TestBlackboxAddRejectsBadSchedule(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)
	out, code := runEon(t, env, "", "add", "--schedule", "every blue moon", "--command", "/bin/x")
	if code == 0 {
		t.Errorf("bad schedule should fail:\n%s", out)
	}
	listOut, _ := runEon(t, env, "", "list")
	if !strings.Contains(listOut, "(no scheduled jobs)") {
		t.Errorf("bad schedule must not write to spool; list:\n%s", listOut)
	}
}

func TestBlackboxUnknownIDExitsNonZero(t *testing.T) {
	t.Parallel()
	env, _, _ := hermeticEnv(t)
	out, code := runEon(t, env, "", "show", "definitely-not-a-real-id")
	if code == 0 {
		t.Errorf("show on unknown ID should exit non-zero:\n%s", out)
	}
}
