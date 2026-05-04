package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rednafi/eon/internal/origin"
)

// fakeOrigin is a minimal Origin used to assemble a Manager without touching
// real cron files. The CLI doesn't care which backend a job came from, so a
// single fake covers list/show/delete behavior.
type fakeOrigin struct {
	jobs    []origin.Job
	deleted []string
}

func (f *fakeOrigin) Name() string { return "fake" }
func (f *fakeOrigin) List(_ context.Context) ([]origin.Job, error) {
	return append([]origin.Job(nil), f.jobs...), nil
}
func (f *fakeOrigin) Delete(_ context.Context, id string) error {
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return origin.ErrNotFound
}

func newFakeManager(jobs ...origin.Job) (*origin.Manager, *fakeOrigin) {
	f := &fakeOrigin{jobs: jobs}
	return origin.NewManager(f), f
}

func TestRunListEmpty(t *testing.T) {
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"list"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "(no scheduled jobs)") {
		t.Errorf("missing empty-state message: %q", out.String())
	}
}

func TestRunListHidesSystemByDefault(t *testing.T) {
	mgr, _ := newFakeManager(
		origin.Job{ID: "fake:user", Kind: "fake", Name: "user-job", Schedule: "@daily"},
		origin.Job{ID: "fake:sys", Kind: "fake", Name: "sys-job", Schedule: "@daily", System: true},
	)
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"list"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "sys-job") {
		t.Errorf("default list should hide system jobs:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "user-job") {
		t.Errorf("user job missing:\n%s", out.String())
	}
}

func TestRunListAllShowsSystem(t *testing.T) {
	mgr, _ := newFakeManager(
		origin.Job{ID: "fake:user", Kind: "fake", Name: "user-job"},
		origin.Job{ID: "fake:sys", Kind: "fake", Name: "sys-job", System: true},
	)
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"list", "--all"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "sys-job") {
		t.Errorf("--all should surface system jobs:\n%s", out.String())
	}
}

func TestRunListJSON(t *testing.T) {
	mgr, _ := newFakeManager(origin.Job{ID: "fake:1", Kind: "fake", Name: "first", Schedule: "@daily"})
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"list", "--json"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var got []origin.Job
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "fake:1" {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestRunShowResolvesByPrefix(t *testing.T) {
	mgr, _ := newFakeManager(
		origin.Job{ID: "fake:com.example.alpha", Name: "alpha"},
		origin.Job{ID: "fake:com.example.beta", Name: "beta"},
	)
	var out bytes.Buffer
	if code := Run(context.Background(), mgr, []string{"show", "alpha"}, nil, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "com.example.alpha") {
		t.Errorf("unexpected output: %s", out.String())
	}
}

func TestRunDeleteWithYesFlag(t *testing.T) {
	mgr, fake := newFakeManager(origin.Job{ID: "fake:to-go", Name: "to-go"})
	var out bytes.Buffer
	code := Run(context.Background(), mgr, []string{"delete", "to-go", "--yes"}, nil, &out, &out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "fake:to-go" {
		t.Errorf("delete not invoked: %v", fake.deleted)
	}
}

func TestRunDeletePromptDeniedKeepsJob(t *testing.T) {
	mgr, fake := newFakeManager(origin.Job{ID: "fake:keep", Name: "keep"})
	var out bytes.Buffer
	stdin := strings.NewReader("n\n")
	code := Run(context.Background(), mgr, []string{"delete", "keep"}, stdin, &out, &out)
	if code == 0 {
		t.Errorf("expected non-zero exit on abort, got 0")
	}
	if len(fake.deleted) != 0 {
		t.Errorf("delete should have been skipped: %v", fake.deleted)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	mgr, _ := newFakeManager()
	var out bytes.Buffer
	code := Run(context.Background(), mgr, []string{"bogus"}, nil, &out, &out)
	if code != 2 {
		t.Errorf("unknown command should exit 2, got %d", code)
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
