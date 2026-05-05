// Pure parsing/rendering helpers for systemd unit files. Heavy lifting is
// delegated to github.com/coreos/go-systemd — the canonical Go library
// for unit-file I/O, used by Kubernetes node tooling and Docker.

package systemd

import (
	"io"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/unit"

	"github.com/rednafi/eon/cron"
)

// parseUnit reads a unit file into a flat map keyed by "Section.Key".
// When the same key appears more than once in the same section (e.g.
// multiple OnCalendar= lines), the last write wins. Use parseUnitMulti
// when callers need every value.
func parseUnit(content string) map[string]string {
	flat, _ := parseUnitMulti(content)
	out := make(map[string]string, len(flat))
	for k, v := range flat {
		if len(v) > 0 {
			out[k] = v[len(v)-1]
		}
	}
	return out
}

// parseUnitMulti is the multi-valued sibling of parseUnit. Each key maps
// to the slice of values seen, in source order. The error path surfaces
// parse failures (e.g. a malformed line) but partial results are still
// returned so a caller can show what *did* parse alongside the warning.
//
// Entries with an empty Name (a stray "=value" line) are dropped — they
// are malformed and would land under a key like "Service." which isn't
// useful to any caller.
func parseUnitMulti(content string) (map[string][]string, error) {
	opts, err := unit.DeserializeOptions(strings.NewReader(content))
	out := map[string][]string{}
	for _, o := range opts {
		if o.Name == "" {
			continue
		}
		key := o.Section + "." + o.Name
		out[key] = append(out[key], o.Value)
	}
	return out, err
}

// prefixed returns p+s when s is non-empty, "" otherwise. Lets cmp.Or
// chains express conditional fallbacks ("every <v>" only if v is set).
func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

// systemdLabel derives a label from a command, prefixed with "eon-" so
// the source of an eon-created unit is obvious in `systemctl list-timers`.
func systemdLabel(command string) string {
	short := cron.CommandShortName(command)
	short = strings.ReplaceAll(short, "/", "-")
	if short == "" {
		short = "job"
	}
	return "eon-" + short
}

// renderTimer emits a minimal [Unit]+[Timer]+[Install] body. Goes through
// unit.Serialize so the output matches systemd's own escaping rules and
// round-trips through DeserializeOptions.
func renderTimer(label string, every time.Duration, descriptor string) string {
	opts := []*unit.UnitOption{
		unit.NewUnitOption("Unit", "Description", "eon-managed timer for "+label),
	}
	switch {
	case every > 0:
		opts = append(opts,
			unit.NewUnitOption("Timer", "OnUnitActiveSec", every.String()),
			unit.NewUnitOption("Timer", "OnBootSec", every.String()),
		)
	case descriptor != "":
		opts = append(opts, unit.NewUnitOption("Timer", "OnCalendar", descriptor))
	}
	opts = append(opts,
		unit.NewUnitOption("Timer", "Persistent", "true"),
		unit.NewUnitOption("Install", "WantedBy", "timers.target"),
	)
	return serializeUnit(opts)
}

// renderService emits the matching .service body for a Timer.
func renderService(label, command string) string {
	opts := []*unit.UnitOption{
		unit.NewUnitOption("Unit", "Description", "eon-managed service for "+label),
		unit.NewUnitOption("Service", "Type", "oneshot"),
		unit.NewUnitOption("Service", "ExecStart", command),
	}
	return serializeUnit(opts)
}

func serializeUnit(opts []*unit.UnitOption) string {
	body, _ := io.ReadAll(unit.Serialize(opts))
	return string(body)
}
