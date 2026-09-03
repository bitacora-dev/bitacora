//go:build linux && !cgo

package journald

import "fmt"

func openSystemdJournal(cursor string) (Reader, error) {
	return nil, fmt.Errorf("the systemd journal requires cgo and libsystemd")
}
