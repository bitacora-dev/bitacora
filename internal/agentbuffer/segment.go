package agentbuffer

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// segmentWriter is the active WAL segment: an uncompressed, append-only
// file, fsynced after every write. It stays uncompressed while active so
// each Append can be durable immediately — compressing would mean
// buffering in memory until rotation, which is exactly the crash window
// ADR-0008's durability requirement rules out.
type segmentWriter struct {
	path     string
	f        *os.File
	rawBytes int
	items    []Item // kept in memory too, so seal() doesn't need to re-read the file
}

func newSegmentWriter(dir string) (*segmentWriter, error) {
	path := filepath.Join(dir, newULIDName(".wal"))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	return &segmentWriter{path: path, f: f}, nil
}

func (w *segmentWriter) write(item Item) error {
	line, err := json.Marshal(item)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	if _, err := w.f.Write(line); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}

	w.rawBytes += len(line)
	w.items = append(w.items, item)
	return nil
}

// seal compresses the segment's accumulated items into a .wal.zst file,
// removes the uncompressed .wal file, and returns the sealed segment's
// metadata.
func (w *segmentWriter) seal() (sealedSegment, error) {
	if err := w.f.Close(); err != nil {
		return sealedSegment{}, err
	}

	sealedPath := w.path[:len(w.path)-len(".wal")] + ".wal.zst"
	if err := writeSealedSegment(sealedPath, w.items); err != nil {
		return sealedSegment{}, err
	}
	if err := os.Remove(w.path); err != nil {
		return sealedSegment{}, err
	}

	seg := sealedSegment{path: sealedPath, count: len(w.items)}
	if len(w.items) > 0 {
		seg.minSeq = w.items[0].Seq
		seg.maxSeq = w.items[len(w.items)-1].Seq
	}
	if info, err := os.Stat(sealedPath); err == nil {
		seg.byteSize = info.Size()
	}
	return seg, nil
}

// rewrite replaces the active segment file's contents with exactly
// w.items — used after removing individual items (capacity discard, Ack)
// so the on-disk file matches the in-memory item list. The file is opened
// O_APPEND, so truncating it to empty and writing lands the new content at
// offset 0 without needing a explicit Seek.
func (w *segmentWriter) rewrite() error {
	if err := w.f.Truncate(0); err != nil {
		return err
	}

	var buf []byte
	for _, it := range w.items {
		line, err := json.Marshal(it)
		if err != nil {
			return err
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}

	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	w.rawBytes = len(buf)
	return nil
}
