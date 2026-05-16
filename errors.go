package eon

import "errors"

// Sentinel errors are part of the public contract.
//
// Front-ends map them to exit codes.
// Library consumers use errors.Is to branch.
// Add new entries deliberately.
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
