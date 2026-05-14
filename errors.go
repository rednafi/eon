package eon

import "errors"

// Sentinel errors. Front-ends map these to exit codes; library
// consumers use [errors.Is] to branch. New entries here are part of
// the public contract — add deliberately.
var (
	ErrNotFound      = errors.New("eon: not found")
	ErrConflict      = errors.New("eon: conflict")
	ErrDaemonDown    = errors.New("eon: daemon not running")
	ErrDaemonUp      = errors.New("eon: daemon already running")
	ErrInvalidCron   = errors.New("eon: invalid cron expression")
	ErrInvalidTime   = errors.New("eon: invalid time")
	ErrInvalidSpec   = errors.New("eon: invalid job spec")
	ErrUnsupportedOS = errors.New("eon: unsupported OS")
)
