// Package etccron is a read-only cron.Source for the system crontab
// (/etc/crontab) and the run-parts drop-in directory (/etc/cron.d). The
// package compiles on every platform; whether it's wired into the
// composed Manager is the per-platform factory's call (today: Linux only).
//
// The pure parsing core lives in parser.go; this file holds the
// imperative shell that reads files off disk and feeds bytes in.
package etccron

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	cronspec "github.com/robfig/cron/v3"

	"github.com/rednafi/eon/cron"
)

// Compile-time guard: EtcCron satisfies cron.Source.
var _ cron.Source = (*EtcCron)(nil)

// EtcCron is the cron.Source for /etc/crontab plus /etc/cron.d/* drop-ins.
// Always read-only — these files are owned by root or the package
// manager; eon won't offer to delete them.
//
// The system crontab uses a six-field syntax (the same five schedule
// fields plus an explicit user column) which the per-user crontab
// parser doesn't handle. We keep this source distinct so subtle parser
// bugs in one don't bleed into the other.
type EtcCron struct {
	// MainPath is the /etc/crontab single-file source.
	MainPath string
	// DropInDir is the /etc/cron.d directory of fragment files.
	DropInDir string
	parser    cronspec.Parser
}

// New returns a source for the standard system locations.
func New() *EtcCron {
	return &EtcCron{
		MainPath:  "/etc/crontab",
		DropInDir: "/etc/cron.d",
		parser:    cronspec.NewParser(cronspec.Minute | cronspec.Hour | cronspec.Dom | cronspec.Month | cronspec.Dow | cronspec.Descriptor),
	}
}

// Name implements cron.Source.
func (e *EtcCron) Name() string { return "crontab-system" }

// Scope implements cron.Source. /etc/crontab and /etc/cron.d are owned by
// root or the package manager, so we never offer to mutate them.
func (e *EtcCron) Scope() cron.Scope { return cron.ScopeSystem }

// List implements cron.Source. Errors reading individual files are
// tolerated — surfacing the readable ones is more useful than failing the
// whole list.
func (e *EtcCron) List(_ context.Context) ([]cron.Job, error) {
	var jobs []cron.Job
	if data, err := os.ReadFile(e.MainPath); err == nil {
		got, _ := parseEtcCrontab(e.parser, e.MainPath, data, "main")
		jobs = append(jobs, got...)
	}
	if entries, err := os.ReadDir(e.DropInDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			// Cron's run-parts skips anything with a "." in the name
			// (config files etc) — match that behaviour to avoid
			// surfacing things cron itself ignores.
			if strings.Contains(ent.Name(), ".") {
				continue
			}
			full := filepath.Join(e.DropInDir, ent.Name())
			if data, err := os.ReadFile(full); err == nil {
				got, _ := parseEtcCrontab(e.parser, full, data, ent.Name())
				jobs = append(jobs, got...)
			}
		}
	}
	slices.SortFunc(jobs, func(a, b cron.Job) int { return cmp.Compare(a.ID, b.ID) })
	return jobs, nil
}

// Delete is a no-op: /etc/crontab and /etc/cron.d entries are owned by
// root or the system package manager. Returning cron.ErrNotFound rather
// than a permission error lets cron.Manager fall through to whichever
// source does own the ID.
func (e *EtcCron) Delete(_ context.Context, _ string) error {
	return cron.ErrNotFound
}
