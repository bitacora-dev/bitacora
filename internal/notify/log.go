package notify

import (
	"context"
	"fmt"
	"io"
	"os"
)

// LogNotifier writes to the system log — ADR-0009: "siempre activo, no
// configurable." Every deployment gets this one whether or not any other
// notifier is set up.
type LogNotifier struct {
	// Writer defaults to os.Stderr.
	Writer io.Writer
}

// Notify implements Notifier. It never fails — writing to a local log
// stream isn't expected to error in any way a caller could usefully react
// to, and this is the one notifier ADR-0009 says must always be active.
func (l *LogNotifier) Notify(ctx context.Context, notif Notification) error {
	w := l.Writer
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintln(w, notif.Body())
	return nil
}
