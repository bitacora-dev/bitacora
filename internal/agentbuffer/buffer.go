// Package agentbuffer implements the agent's local outbound WAL
// (ADR-0008): metrics, events and log lines are appended durably to disk
// before the agent ever tries to send them, so a hub outage — even across
// an agent or host restart — never loses what happened in the meantime.
package agentbuffer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Priority controls discard order when the buffer is over capacity
// (ADR-0008): log lines go first, then raw metrics, and events are never
// discarded. Lower value = discarded first.
type Priority int

const (
	PriorityLogLine Priority = iota
	PriorityMetric
	PriorityEvent // never discarded
)

// Default capacity (ADR-0008): 2 hours or 256 MB, whichever comes first.
const (
	DefaultMaxAge       = 2 * time.Hour
	DefaultMaxBytes     = 256 << 20
	DefaultSegmentBytes = 4 << 20
)

// Item is one buffered record. Exactly one of Metric/Event/LogLine is set.
type Item struct {
	Seq      uint64          `json:"seq"`
	Priority Priority        `json:"priority"`
	TS       time.Time       `json:"ts"`
	Metric   *schema.Metric  `json:"metric,omitempty"`
	Event    *schema.Event   `json:"event,omitempty"`
	LogLine  *schema.LogLine `json:"log_line,omitempty"`
}

// Buffer is the on-disk WAL. Safe for concurrent use.
type Buffer struct {
	dir          string
	segmentBytes int
	maxAge       time.Duration
	maxBytes     int64

	mu       sync.Mutex
	nextSeq  uint64
	active   *segmentWriter
	sealed   []sealedSegment // oldest first
	totalLen int             // count of unacked items across all segments, for quick empty checks
}

type sealedSegment struct {
	path     string // absolute path to the .wal.zst file
	minSeq   uint64
	maxSeq   uint64
	count    int
	byteSize int64 // compressed size on disk
}

// Option configures a Buffer.
type Option func(*Buffer)

// WithSegmentBytes overrides the 4 MB default segment rotation size.
func WithSegmentBytes(n int) Option { return func(b *Buffer) { b.segmentBytes = n } }

// WithCapacity overrides the default 2h/256MB capacity.
func WithCapacity(maxAge time.Duration, maxBytes int64) Option {
	return func(b *Buffer) { b.maxAge = maxAge; b.maxBytes = maxBytes }
}

// Open opens (creating if needed) a Buffer at dir, recovering any segments
// left over from a previous run — including an unclean shutdown, which is
// exactly the case ADR-0008 requires this to survive.
func Open(dir string, opts ...Option) (*Buffer, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating buffer dir %s: %w", dir, err)
	}

	b := &Buffer{
		dir:          dir,
		segmentBytes: DefaultSegmentBytes,
		maxAge:       DefaultMaxAge,
		maxBytes:     DefaultMaxBytes,
	}
	for _, opt := range opts {
		opt(b)
	}

	if err := b.recover(); err != nil {
		return nil, fmt.Errorf("recovering buffer at %s: %w", dir, err)
	}
	return b, nil
}

// Close seals the active segment (if any) so nothing is lost, without
// requiring a clean shutdown to matter — recover() would reconstruct an
// unsealed segment just as well, but sealing on Close keeps the directory
// tidy in the common case.
func (b *Buffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == nil {
		return nil
	}
	seg, err := b.active.seal()
	if err != nil {
		return err
	}
	b.sealed = append(b.sealed, seg)
	b.active = nil
	return nil
}

// Append durably writes item to the WAL, assigning it the next sequence
// number, and returns that sequence. It fsyncs before returning: a crash
// immediately after Append returns must not lose the item.
func (b *Buffer) Append(item Item) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSeq++
	item.Seq = b.nextSeq

	if b.active == nil {
		w, err := newSegmentWriter(b.dir)
		if err != nil {
			return 0, err
		}
		b.active = w
	}

	if err := b.active.write(item); err != nil {
		return 0, err
	}
	b.totalLen++

	if b.active.rawBytes >= b.segmentBytes {
		seg, err := b.active.seal()
		if err != nil {
			return 0, err
		}
		b.sealed = append(b.sealed, seg)
		b.active = nil
	}

	return item.Seq, nil
}

// Len returns how many unacknowledged items are currently buffered.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalLen
}

// segmentPaths returns every segment file (sealed, in order, then the
// active one if present) — used by recovery and by iteration for backfill.
func (b *Buffer) segmentFiles() []string {
	var paths []string
	for _, s := range b.sealed {
		paths = append(paths, s.path)
	}
	if b.active != nil {
		paths = append(paths, b.active.path)
	}
	return paths
}

func (b *Buffer) recover() error {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return err
	}

	var sealedPaths, activePaths []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".wal.zst"):
			sealedPaths = append(sealedPaths, filepath.Join(b.dir, name))
		case strings.HasSuffix(name, ".wal"):
			activePaths = append(activePaths, filepath.Join(b.dir, name))
		}
	}
	sort.Strings(sealedPaths) // ULID names sort chronologically
	sort.Strings(activePaths)

	for _, p := range sealedPaths {
		seg, err := inspectSealedSegment(p)
		if err != nil {
			return fmt.Errorf("inspecting sealed segment %s: %w", p, err)
		}
		b.sealed = append(b.sealed, seg)
		b.totalLen += seg.count
		if seg.maxSeq > b.nextSeq {
			b.nextSeq = seg.maxSeq
		}
	}

	// An unsealed .wal file left over from an unclean shutdown: seal it
	// now so it's treated uniformly with everything else, instead of
	// leaving crash recovery as a special code path elsewhere.
	for _, p := range activePaths {
		seg, err := sealOrphanedActiveSegment(p)
		if err != nil {
			return fmt.Errorf("sealing orphaned segment %s: %w", p, err)
		}
		if seg.count == 0 {
			continue
		}
		b.sealed = append(b.sealed, seg)
		b.totalLen += seg.count
		if seg.maxSeq > b.nextSeq {
			b.nextSeq = seg.maxSeq
		}
	}

	return nil
}

func inspectSealedSegment(path string) (sealedSegment, error) {
	items, err := readSealedSegment(path)
	if err != nil {
		return sealedSegment{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return sealedSegment{}, err
	}
	seg := sealedSegment{path: path, count: len(items), byteSize: info.Size()}
	if len(items) > 0 {
		seg.minSeq = items[0].Seq
		seg.maxSeq = items[len(items)-1].Seq
	}
	return seg, nil
}

func readSealedSegment(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec, err := zstd.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	var items []Item
	scanner := bufio.NewScanner(dec)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var it Item
		if err := json.Unmarshal(line, &it); err != nil {
			return nil, fmt.Errorf("parsing segment record: %w", err)
		}
		items = append(items, it)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func sealOrphanedActiveSegment(path string) (sealedSegment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return sealedSegment{}, err
	}

	var items []Item
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		var it Item
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			// A record can be truncated if the process died mid-write.
			// Stop here rather than fail recovery over the last, partial
			// record — everything before it is still durable.
			break
		}
		items = append(items, it)
	}

	sealedPath := strings.TrimSuffix(path, ".wal") + ".wal.zst"
	if err := writeSealedSegment(sealedPath, items); err != nil {
		return sealedSegment{}, err
	}
	if err := os.Remove(path); err != nil {
		return sealedSegment{}, err
	}

	seg := sealedSegment{path: sealedPath, count: len(items)}
	if len(items) > 0 {
		seg.minSeq = items[0].Seq
		seg.maxSeq = items[len(items)-1].Seq
	}
	if info, err := os.Stat(sealedPath); err == nil {
		seg.byteSize = info.Size()
	}
	return seg, nil
}

func writeSealedSegment(path string, items []Item) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	enc, err := zstd.NewWriter(tmp)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	for _, it := range items {
		line, err := json.Marshal(it)
		if err != nil {
			enc.Close()
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
		if _, err := enc.Write(append(line, '\n')); err != nil {
			enc.Close()
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	if err := enc.Close(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func newULIDName(suffix string) string {
	return ulid.Make().String() + suffix
}
