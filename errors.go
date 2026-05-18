package eon

import "errors"

// Sentinel errors are part of the public contract.
//
// Front-ends map them to exit codes.
// Library consumers use errors.Is to branch.
// Add new entries deliberately.
var (
	// ErrNotFound means a requested job or run does not exist.
	ErrNotFound = errors.New("eon: not found")

	// ErrConflict means an operation conflicts with current state.
	ErrConflict = errors.New("eon: conflict")

	// ErrDaemonDown means the daemon is not running.
	ErrDaemonDown = errors.New("eon: daemon not running")

	// ErrDaemonUp means another daemon instance is already running.
	ErrDaemonUp = errors.New("eon: daemon already running")

	// ErrInvalidCron means a cron expression is not accepted.
	ErrInvalidCron = errors.New("eon: invalid cron expression")

	// ErrInvalidTime means a one-shot time expression is invalid.
	ErrInvalidTime = errors.New("eon: invalid time")

	// ErrInvalidSpec means a job spec violates creation invariants.
	ErrInvalidSpec = errors.New("eon: invalid job spec")

	// ErrUnsupportedOS means the requested supervisor operation is unavailable.
	ErrUnsupportedOS = errors.New("eon: unsupported OS")
)
