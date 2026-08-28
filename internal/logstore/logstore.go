// Package logstore implements Capa 2 of ADR-0003: raw log lines, accumulated
// per (host_id, source), compressed with zstd and written as immutable
// blocks under baseDir/<host_id>/<YYYY>/<MM>/<DD>/<ulid>.zst. Each block
// carries a sidecar <ulid>.meta.json with exactly the metadata ADR-0003
// says belongs in the relational index (block_id, host_id, source, ts_min,
// ts_max, unit/container, n_lines, levels_bitmap, path, size_raw,
// size_compressed) — so the index is reconstructible by scanning the
// directory (see ScanIndex), never dependent on the relational DB surviving.
package logstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// Default flush thresholds (ADR-0003): ~5 MB accumulated or 5 minutes,
// whichever comes first.
const (
	DefaultMaxBytes = 5 * 1024 * 1024
	DefaultMaxAge   = 5 * time.Minute
)

// levelBit maps a log line's Level to one bit of BlockMeta.LevelsBitmap.
// Best-effort: unrecognized levels simply don't set a bit.
var levelBit = map[string]uint32{
	"debug":    1 << 0,
	"info":     1 << 1,
	"notice":   1 << 2,
	"warn":     1 << 3,
	"warning":  1 << 3,
	"error":    1 << 4,
	"critical": 1 << 5,
}

// BlockMeta is the metadata ADR-0003 says the relational index stores for
// a block — and, via its sidecar file, everything ScanIndex needs to
// reconstruct that index without the database.
type BlockMeta struct {
	BlockID        string    `json:"block_id"`
	HostID         string    `json:"host_id"`
	Source         string    `json:"source"`
	TSMin          time.Time `json:"ts_min"`
	TSMax          time.Time `json:"ts_max"`
	Unit           string    `json:"unit,omitempty"`
	NLines         int       `json:"n_lines"`
	LevelsBitmap   uint32    `json:"levels_bitmap"`
	Path           string    `json:"path"` // relative to the store's base dir
	SizeRaw        int64     `json:"size_raw"`
	SizeCompressed int64     `json:"size_compressed"`
}

type bufferKey struct{ hostID, source string }

type buffer struct {
	lines    []schema.LogLine
	rawBytes int
	firstAt  time.Time
}

// Store accumulates log lines per (host_id, source) and flushes each
// accumulator into a compressed block once it crosses the size or age
// threshold.
type Store struct {
	baseDir  string
	maxBytes int
	maxAge   time.Duration
	now      func() time.Time

	mu      sync.Mutex
	buffers map[bufferKey]*buffer
}

// Option configures a Store.
type Option func(*Store)

// WithClock overrides the time source, for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// WithLimits overrides the default flush thresholds.
func WithLimits(maxBytes int, maxAge time.Duration) Option {
	return func(s *Store) { s.maxBytes = maxBytes; s.maxAge = maxAge }
}

// NewStore returns a Store that writes blocks under baseDir.
func NewStore(baseDir string, opts ...Option) *Store {
	s := &Store{
		baseDir:  baseDir,
		maxBytes: DefaultMaxBytes,
		maxAge:   DefaultMaxAge,
		now:      time.Now,
		buffers:  make(map[bufferKey]*buffer),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Append buffers one log line. If this crosses the size or age threshold
// for its (host_id, source) accumulator, the block is flushed immediately
// and its metadata returned; otherwise meta is nil, meaning "buffered, not
// written yet".
func (s *Store) Append(line schema.LogLine) (*BlockMeta, error) {
	if err := line.Validate(); err != nil {
		return nil, fmt.Errorf("invalid log line: %w", err)
	}

	s.mu.Lock()
	key := bufferKey{hostID: line.HostID, source: line.Source}
	buf, ok := s.buffers[key]
	if !ok {
		buf = &buffer{firstAt: s.now()}
		s.buffers[key] = buf
	}
	buf.lines = append(buf.lines, line)
	buf.rawBytes += len(line.Message) + 1

	shouldFlush := buf.rawBytes >= s.maxBytes || s.now().Sub(buf.firstAt) >= s.maxAge
	s.mu.Unlock()

	if !shouldFlush {
		return nil, nil
	}
	return s.Flush(key.hostID, key.source)
}

// Flush forces whatever's buffered for (hostID, source) to write out now,
// regardless of thresholds. Returns nil, nil if there was nothing pending.
func (s *Store) Flush(hostID, source string) (*BlockMeta, error) {
	s.mu.Lock()
	key := bufferKey{hostID: hostID, source: source}
	buf, ok := s.buffers[key]
	if !ok || len(buf.lines) == 0 {
		s.mu.Unlock()
		return nil, nil
	}
	delete(s.buffers, key)
	s.mu.Unlock()

	return s.writeBlock(hostID, source, buf.lines)
}

// FlushAll forces every pending accumulator to flush — e.g. on shutdown, so
// nothing buffered in memory is lost.
func (s *Store) FlushAll() ([]BlockMeta, error) {
	s.mu.Lock()
	keys := make([]bufferKey, 0, len(s.buffers))
	for k := range s.buffers {
		keys = append(keys, k)
	}
	s.mu.Unlock()

	var metas []BlockMeta
	for _, k := range keys {
		meta, err := s.Flush(k.hostID, k.source)
		if err != nil {
			return metas, err
		}
		if meta != nil {
			metas = append(metas, *meta)
		}
	}
	return metas, nil
}

func (s *Store) writeBlock(hostID, source string, lines []schema.LogLine) (*BlockMeta, error) {
	id := ulid.Make().String()

	var raw bytes.Buffer
	tsMin, tsMax := lines[0].TS, lines[0].TS
	var bitmap uint32
	unit := lines[0].UnitOrContainer

	for _, l := range lines {
		raw.WriteString(l.Message)
		raw.WriteByte('\n')
		if l.TS.Before(tsMin) {
			tsMin = l.TS
		}
		if l.TS.After(tsMax) {
			tsMax = l.TS
		}
		if bit, ok := levelBit[strings.ToLower(l.Level)]; ok {
			bitmap |= bit
		}
	}

	day := tsMin.UTC()
	relDir := filepath.Join(hostID, fmt.Sprintf("%04d", day.Year()), fmt.Sprintf("%02d", day.Month()), fmt.Sprintf("%02d", day.Day()))
	absDir := filepath.Join(s.baseDir, relDir)
	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating block dir: %w", err)
	}

	compressed, err := compressZstd(raw.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compressing block: %w", err)
	}

	relPath := filepath.Join(relDir, id+".zst")
	if err := writeAtomic(filepath.Join(s.baseDir, relPath), compressed); err != nil {
		return nil, fmt.Errorf("writing block: %w", err)
	}

	meta := BlockMeta{
		BlockID:        id,
		HostID:         hostID,
		Source:         source,
		TSMin:          tsMin,
		TSMax:          tsMax,
		Unit:           unit,
		NLines:         len(lines),
		LevelsBitmap:   bitmap,
		Path:           relPath,
		SizeRaw:        int64(raw.Len()),
		SizeCompressed: int64(len(compressed)),
	}

	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling block metadata: %w", err)
	}
	metaPath := filepath.Join(s.baseDir, relDir, id+".meta.json")
	if err := writeAtomic(metaPath, encoded); err != nil {
		return nil, fmt.Errorf("writing block metadata: %w", err)
	}

	return &meta, nil
}

func compressZstd(data []byte) ([]byte, error) {
	// zstd level 3 (ADR-0003) is klauspost's SpeedDefault — see the
	// package's own doc comment on that constant.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(data, nil), nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
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

// DecompressBlock reads and decompresses a .zst block file, returning its
// raw newline-delimited log lines. Mainly for tests and `bita logs`
// tooling — the hub's search path decompresses candidate blocks the same
// way (ADR-0003).
func DecompressBlock(path string) ([]byte, error) {
	compressed, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading block %s: %w", path, err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(compressed, nil)
}
