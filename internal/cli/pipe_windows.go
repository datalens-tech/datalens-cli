package cli

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func closedOutputPipe(err error) bool {
	return errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, syscall.EPIPE)
}
