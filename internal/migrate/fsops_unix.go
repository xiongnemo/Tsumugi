//go:build !windows

package migrate

import (
	"errors"
	"syscall"
)

// errCrossDevice is what rename reports when source and destination are on different volumes.
var errCrossDevice error = syscall.EXDEV

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
