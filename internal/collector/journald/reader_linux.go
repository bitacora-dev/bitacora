//go:build linux && cgo

// This file requires CGO and libsystemd — sdjournal is a cgo binding over
// libsystemd, and there is no pure-Go implementation of the journal
// binary format. That's a deliberate, scoped exception to ADR-0001's
// general "prohibido en el agente: cualquier dependencia que requiera CGO
// por defecto": ADR-0005 names sdjournal specifically as the journald
// mechanism ("sdjournal con cursor persistido"), and the alternative
// (exec'ing journalctl) would need os/exec from the unprivileged agent
// process, which ADR-0012 reserves for privileged helpers only. Discussed
// and confirmed with the user in-session rather than decided silently.
//
// Only this file pays the CGO cost — journald.go, its tests, and every
// other collector build and test cgo-free, including on macOS.
package journald

import (
	"context"
	"fmt"

	"github.com/coreos/go-systemd/v22/sdjournal"
)

type sdjournalReader struct {
	j *sdjournal.Journal
}

func openSystemdJournal(cursor string) (Reader, error) {
	j, err := sdjournal.NewJournal()
	if err != nil {
		return nil, fmt.Errorf("opening systemd journal: %w", err)
	}

	if cursor != "" {
		if err := j.SeekCursor(cursor); err != nil {
			j.Close()
			return nil, fmt.Errorf("seeking to cursor: %w", err)
		}
		// SeekCursor positions the read pointer AT the given entry; advance
		// once so the first Next() returns the entry after the last one
		// already processed, not a repeat of it.
		if _, err := j.Next(); err != nil {
			j.Close()
			return nil, fmt.Errorf("advancing past last cursor: %w", err)
		}
	} else if err := j.SeekTail(); err != nil {
		j.Close()
		return nil, fmt.Errorf("seeking to tail: %w", err)
	}

	return &sdjournalReader{j: j}, nil
}

func (r *sdjournalReader) Next(ctx context.Context) (Entry, bool, error) {
	n, err := r.j.Next()
	if err != nil {
		return Entry{}, false, err
	}
	if n == 0 {
		return Entry{}, false, nil
	}

	je, err := r.j.GetEntry()
	if err != nil {
		return Entry{}, false, fmt.Errorf("reading entry: %w", err)
	}

	return Entry{
		Fields:       je.Fields,
		RealtimeUsec: je.RealtimeTimestamp,
		Cursor:       je.Cursor,
	}, true, nil
}

func (r *sdjournalReader) Close() error {
	return r.j.Close()
}
