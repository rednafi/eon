//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rednafi/eon/cron"
)

// TestCLIEndToEndCrontab exercises `eon list`, `eon show`, and
// `eon delete --yes` against a real Linux crontab. Gated by EON_RUN_REAL_CRON
// so it only runs in the container CI job.
func TestCLIEndToEndCrontab(t *testing.T) {
	if os.Getenv("EON_RUN_REAL_CRON") != "1" {
		t.Skip("set EON_RUN_REAL_CRON=1 to run (mutates the user's crontab)")
	}
	if _, err := exec.LookPath("crontab"); err != nil {
		t.Skip("no crontab binary on this host")
	}

	original, hadOriginal := snapshotUserCrontab(t)
	t.Cleanup(func() { restoreUserCrontab(t, original, hadOriginal) })

	want := "*/5 * * * * /bin/echo eon-cli-test\n@daily /bin/true\n"
	if err := installUserCrontab(want); err != nil {
		t.Fatalf("install: %v", err)
	}

	mgr := cron.NewManager(cron.NewCrontab())
	ctx := context.Background()

	var out bytes.Buffer
	if code := Run(ctx, mgr, []string{"list"}, nil, &out, &out); code != 0 {
		t.Fatalf("list exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "echo eon-cli-test") && !strings.Contains(out.String(), "@daily") {
		t.Errorf("list missing expected entries:\n%s", out.String())
	}

	out.Reset()
	if code := Run(ctx, mgr, []string{"show", "eon-cli-test"}, nil, &out, &out); code != 0 {
		t.Fatalf("show exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "echo eon-cli-test") {
		t.Errorf("show didn't surface command:\n%s", out.String())
	}

	out.Reset()
	if code := Run(ctx, mgr, []string{"delete", "eon-cli-test", "--yes"}, nil, &out, &out); code != 0 {
		t.Fatalf("delete exit %d: %s", code, out.String())
	}

	jobs, _ := mgr.List(ctx)
	for _, j := range jobs {
		if strings.Contains(j.Command, "eon-cli-test") {
			t.Errorf("deleted job still present: %+v", j)
		}
	}
}

func snapshotUserCrontab(t *testing.T) ([]byte, bool) {
	t.Helper()
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "no crontab") {
			return nil, false
		}
		t.Logf("crontab -l: %s", strings.TrimSpace(string(out)))
		return nil, false
	}
	return out, true
}

func restoreUserCrontab(t *testing.T, content []byte, had bool) {
	t.Helper()
	if !had {
		_ = exec.Command("crontab", "-r").Run()
		return
	}
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = bytes.NewReader(content)
	if err := cmd.Run(); err != nil {
		t.Errorf("restore crontab: %v", err)
	}
}

func installUserCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &exec.ExitError{Stderr: out}
	}
	return nil
}
