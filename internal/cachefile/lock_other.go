//go:build !unix

package cachefile

import "os"

// Without flock the lock is a no-op and every instance fetches for itself,
// which is the behaviour from before the lock existed.
func tryLock(*os.File) (ok, supported bool) { return false, false }
