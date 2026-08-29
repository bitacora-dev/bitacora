// Package pstore ingests /sys/fs/pstore (ADR-0011): the kernel reserves a
// small region of RAM that survives a reboot, and writes an oops or panic
// there when one happens. It's the one mechanism that would have captured
// the cause of the incident that motivates this ADR, had there been a
// silent oops instead of a truly hard hang.
package pstore

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DefaultRoot is where the kernel exposes pstore entries in production.
const DefaultRoot = "/sys/fs/pstore"

// MaxExcerptBytes bounds how much of a dump's content rides along on the
// Event as an attribute. The Event is not the durable copy of the dump —
// see this package's README for why archiving the full content isn't
// built here yet — but a bounded excerpt is enough to see what happened
// without leaving the timeline.
const MaxExcerptBytes = 4000

// Entry is one file under pstore's root.
type Entry struct {
	Name    string // the pstore filename, e.g. "dmesg-efi-123456789"
	Path    string
	Content []byte
}

// isDmesgEntry reports whether name is a kernel dmesg/oops dump, as
// opposed to pstore's other record types (console-*, pmsg-*, ftrace-*)
// that aren't "el oops o el panic" ADR-0011 is after.
func isDmesgEntry(name string) bool {
	return strings.HasPrefix(name, "dmesg-")
}

// List returns every dmesg entry under root, oldest filename first — the
// kernel's own naming (backend-id, monotonically assigned) sorts that way
// for a single backend, which is the common case.
func List(root string) ([]Entry, error) {
	files, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading pstore root %s: %w", root, err)
	}

	var names []string
	for _, f := range files {
		if f.IsDir() || !isDmesgEntry(f.Name()) {
			continue
		}
		names = append(names, f.Name())
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		content, err := os.ReadFile(path)
		if err != nil {
			continue // one unreadable entry shouldn't hide the rest
		}
		entries = append(entries, Entry{Name: name, Path: path, Content: content})
	}
	return entries, nil
}

// ToEvent converts one pstore entry into a kernel.crash_dump Event
// (ADR-0011).
func ToEvent(hostID string, e Entry, now time.Time) schema.Event {
	excerpt := e.Content
	truncated := false
	if len(excerpt) > MaxExcerptBytes {
		excerpt = excerpt[:MaxExcerptBytes]
		truncated = true
	}

	attrs := schema.Labels{
		"pstore_file": e.Name,
		"size_bytes":  fmt.Sprintf("%d", len(e.Content)),
		"excerpt":     string(excerpt),
	}
	if truncated {
		attrs["excerpt_truncated"] = "true"
	}

	id, _ := ulid.New(ulid.Timestamp(now), ulid.Monotonic(rand.Reader, 0))
	return schema.Event{
		ID:       id.String(),
		TS:       now,
		HostID:   hostID,
		Source:   "pstore",
		Type:     "kernel.crash_dump",
		Severity: schema.SeverityCritical,
		Title:    fmt.Sprintf("kernel crash dump recovered from pstore (%s)", e.Name),
		Subject:  schema.EventSubject{Kind: "kernel", Name: "pstore"},
		Attrs:    attrs,
		Schema:   schema.CurrentSchemaVersion,
	}
}

// Consume lists every dmesg entry under root, converts each to an Event,
// and removes the file — "limpia la región" (ADR-0011): pstore's backing
// RAM is small and finite, and must be freed for the next crash to have
// somewhere to go. Best-effort per entry: one file that fails to convert
// or remove is reported in errs without blocking the rest.
func Consume(root, hostID string, now time.Time) ([]schema.Event, []error) {
	entries, err := List(root)
	if err != nil {
		return nil, []error{err}
	}

	var events []schema.Event
	var errs []error
	for _, e := range entries {
		events = append(events, ToEvent(hostID, e, now))
		if err := os.Remove(e.Path); err != nil {
			errs = append(errs, fmt.Errorf("removing pstore entry %s: %w", e.Path, err))
		}
	}
	return events, errs
}
