//go:build unix

package cachefile

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock without blocking. flock rather than an
// O_EXCL marker file: the kernel drops it when the holder exits, so a ghx
// killed mid-fetch never leaves its siblings waiting on a lock nobody holds.
func tryLock(f *os.File) (ok, supported bool) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, true
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, true
	}
	return false, false
}
