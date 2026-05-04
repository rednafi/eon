package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rednafi/eon/cron"
)

// fakeOrigin is a minimal Source used to assemble a Manager without touching
// real cron files. The CLI doesn't care which backend a job came from, so a
// single fake covers list/show/delete behaviour.
type fakeOrigin struct {
	jobs    []cron.Job
	deleted []string
}

func (f *fakeOrigin) Name() string      { return "fake" }
func (f *fakeOrigin) Scope() cron.Scope { return cron.ScopeUser }
func (f *fakeOrigin) List(_ context.Context) ([]cron.Job, error) {
	return append([]cron.Job(nil), f.jobs...), nil
}
func (f *fakeOrigin) Delete(_ context.Context, id string) error {
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return cron.ErrNotFound
}

func newFakeManager(jobs ...cron.Job) (*cron.Manager, *fakeOrigin) {
	f := &fakeOrigin{jobs: jobs}
	return cron.NewManager(f), f
}

// runCmd builds the root cobra command, points its IO at the supplied
// buffers, sets argv, and runs it. Returns whatever error (or nil) cobra
// produced — analogous to the old `Run(...) int` but lets tests assert on
// the actual error value.
func runCmd(t *testing.T, mgr *cron.Manager, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	t.Helper()
	root := BuildRoot(mgr)
	root.SetArgs(argv)
	if stdin != nil {
		root.SetIn(stdin)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root.ExecuteContext(context.Background())
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if !strings.Contains(out.String(), "(no scheduled jobs)") {
		t.Errorf("missing empty-state message: %q", out.String())
	}
}

func TestListJSON(t *testing.T) {
	mgr, _ := newFakeManager(cron.Job{ID: "fake:1", Kind: "fake", Name: "first", Schedule: "@daily"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list", "--json"}, nil, &out, &out))
	var got []cron.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "fake:1" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestListHidesSystemByDefault(t *testing.T) {
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:user", Name: "user-job"},
		cron.Job{ID: "fake:sys", Name: "sys-job", Scope: cron.ScopeSystem},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list"}, nil, &out, &out))
	if strings.Contains(out.String(), "sys-job") {
		t.Errorf("default list should hide system jobs:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "user-job") {
		t.Errorf("user job missing:\n%s", out.String())
	}
}

func TestListAllShowsSystem(t *testing.T) {
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:user", Name: "user-job"},
		cron.Job{ID: "fake:sys", Name: "sys-job", Scope: cron.ScopeSystem},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"list", "--all"}, nil, &out, &out))
	if !strings.Contains(out.String(), "sys-job") {
		t.Errorf("--all should surface system jobs:\n%s", out.String())
	}
}

func TestShowResolvesByPrefix(t *testing.T) {
	mgr, _ := newFakeManager(
		cron.Job{ID: "fake:com.example.alpha", Name: "alpha"},
		cron.Job{ID: "fake:com.example.beta", Name: "beta"},
	)
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"show", "alpha"}, nil, &out, &out))
	if !strings.Contains(out.String(), "com.example.alpha") {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestDeleteWithYesFlag(t *testing.T) {
	mgr, fake := newFakeManager(cron.Job{ID: "fake:to-go", Name: "to-go"})
	var out bytes.Buffer
	mustOK(t, runCmd(t, mgr, []string{"delete", "to-go", "--yes"}, nil, &out, &out))
	if len(fake.deleted) != 1 || fake.deleted[0] != "fake:to-go" {
		t.Errorf("delete not invoked: %v", fake.deleted)
	}
}

func TestDeletePromptDeniedKeepsJob(t *testing.T) {
	mgr, fake := newFakeManager(cron.Job{ID: "fake:keep", Name: "keep"})
	var out bytes.Buffer
	stdin := strings.NewReader("n\n")
	err := runCmd(t, mgr, []string{"delete", "keep"}, stdin, &out, &out)
	if !errors.Is(err, errAborted) {
		t.Errorf("want errAborted, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("delete should have been skipped: %v", fake.deleted)
	}
}

func TestDeleteSystemRefused(t *testing.T) {
	mgr, fake := newFakeManager(cron.Job{ID: "fake:sys", Name: "sys", Scope: cron.ScopeSystem})
	var out bytes.Buffer
	err := runCmd(t, mgr, []string{"delete", "sys", "--yes"}, nil, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "system-scope") {
		t.Errorf("system delete should be refused, got %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("system job should not be deleted: %v", fake.deleted)
	}
}

func TestUnknownCommandIsError(t *testing.T) {
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	if err := runCmd(t, mgr, []string{"bogus"}, nil, &out, &out); err == nil {
		t.Errorf("unknown command should produce an error")
	}
}

func TestTailReturnsLastNLines(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "log")
	var b strings.Builder
	for i := range 50 {
		b.WriteString("line ")
		b.WriteRune(rune('A' + i%26))
		b.WriteByte('\n')
	}
	body := b.String()
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var out bytes.Buffer
	if err := tail(&out, f, 5); err != nil {
		t.Fatalf("tail: %v", err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 5 {
		t.Fatalf("want 5 lines, got %d: %v", len(got), got)
	}
}

// silenceCobra returns a no-op Run function so cobra doesn't print its own
// "Error:" prefix on top of the test's expected error formatting. Used in
// individual tests via cobra.Command.SetErrPrefix; left here for future
// reuse.
var _ = silenceCobra

func silenceCobra(c *cobra.Command) { c.SilenceErrors = true }
