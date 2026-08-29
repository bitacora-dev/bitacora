//go:build !linux

package journald

import "fmt"

// openSystemdJournal has no implementation outside Linux — the systemd
// journal doesn't exist elsewhere. The package still builds and its
// Collect-loop/cursor-persistence logic is still testable (against a fake
// Reader); only actually opening the real journal fails, with a clear
// error rather than a missing symbol.
func openSystemdJournal(cursor string) (Reader, error) {
	return nil, fmt.Errorf("the systemd journal is only available on Linux")
}
