package common

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryDiskCacheLease(file *os.File) (bool, error) {
	// Reserve a byte far beyond any possible payload. A Windows exclusive
	// byte-range lock must not prevent independent replay readers.
	lease := windows.Overlapped{Offset: 0xfffffffe, OffsetHigh: 0x7fffffff}
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lease)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return err == nil, err
}
