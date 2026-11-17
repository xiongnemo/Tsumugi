//go:build windows

package migrate

import (
	"errors"
	"syscall"
)

// errorNotSameDevice is ERROR_NOT_SAME_DEVICE, MoveFileEx's answer for a move across volumes.
const errorNotSameDevice syscall.Errno = 17

// errCrossDevice is what rename reports when source and destination are on different volumes.
var errCrossDevice error = errorNotSameDevice

func isCrossDevice(err error) bool {
	return errors.Is(err, errorNotSameDevice)
}
