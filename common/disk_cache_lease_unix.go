//go:build !windows

package common

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// The file descriptor owns the lease. Process exit releases it too, so old
// files from interrupted runs remain eligible for cleanup.
func tryDiskCacheLease(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
