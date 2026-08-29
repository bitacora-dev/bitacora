package blackbox

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// DefaultCapacity is 15 minutes of history at 1 Hz — ADR-0011's retention
// target.
const DefaultCapacity = 900

// Recorder is the preallocated, memory-mapped ring buffer. Record is the
// only hot-path method, and it never allocates: it copies a fixed-size
// encoded Sample directly into the mapped region and advances a counter.
// Durability to disk happens separately, via Sync — ADR-0011's own
// distinction between "written" (in the mapping, fast) and "volcado"
// (msync'd, every 5 s).
type Recorder struct {
	mu         sync.Mutex
	f          *os.File
	data       []byte
	capacity   uint32
	writeIndex uint64
}

// Open opens (creating if needed) the blackbox file at path, sized for
// capacity samples, and memory-maps it. If a file already exists with a
// matching capacity and record layout, recording resumes from its
// writeIndex — an agent restart doesn't reset the ring, since the
// pre-fail window ADR-0011 wants can span a restart. A file that doesn't
// match (different capacity, or a Sample layout from a different build)
// is reinitialized rather than misinterpreted.
func Open(path string, capacity uint32) (*Recorder, error) {
	if capacity == 0 {
		capacity = DefaultCapacity
	}
	size := int64(headerSize) + int64(capacity)*int64(sampleEncodedSize)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o640)
	if err != nil {
		return nil, fmt.Errorf("opening blackbox file %s: %w", path, err)
	}

	writeIndex, err := prepareFile(f, size, capacity)
	if err != nil {
		f.Close()
		return nil, err
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap blackbox file %s: %w", path, err)
	}

	r := &Recorder{f: f, data: data, capacity: capacity, writeIndex: writeIndex}
	r.writeHeaderLocked()
	return r, nil
}

// prepareFile ensures f is exactly size bytes and returns the writeIndex
// to resume from: 0 for a fresh file, or the existing file's own if its
// header matches this build's capacity and record layout.
func prepareFile(f *os.File, size int64, capacity uint32) (uint64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}

	if info.Size() >= headerSize {
		raw := make([]byte, headerSize)
		if _, err := f.ReadAt(raw, 0); err == nil {
			if h, err := decodeHeader(raw); err == nil &&
				h.Capacity == capacity && h.SampleSize == uint32(sampleEncodedSize) {
				if err := f.Truncate(size); err != nil {
					return 0, fmt.Errorf("resizing existing blackbox file: %w", err)
				}
				return h.WriteIndex, nil
			}
		}
	}

	if err := f.Truncate(0); err != nil {
		return 0, fmt.Errorf("resetting blackbox file: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		return 0, fmt.Errorf("preallocating blackbox file: %w", err)
	}
	return 0, nil
}

// Record writes s into the current ring slot and advances writeIndex. It
// never allocates and never touches disk directly — see Sync.
func (r *Recorder) Record(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()

	slot := r.writeIndex % uint64(r.capacity)
	offset := headerSize + int(slot)*sampleEncodedSize
	copy(r.data[offset:offset+sampleEncodedSize], encodeSample(s))

	r.writeIndex++
	r.writeHeaderLocked()
}

func (r *Recorder) writeHeaderLocked() {
	h := header{
		Magic:      magic,
		Version:    formatVersion,
		SampleSize: uint32(sampleEncodedSize),
		Capacity:   r.capacity,
		WriteIndex: r.writeIndex,
	}
	copy(r.data[0:headerSize], encodeHeader(h))
}

// Sync flushes the mapped region to disk (ADR-0011: "volcado a disco cada
// 5 s [...] con escritura atómica"). Everything Record wrote before this
// call is durable once Sync returns; a crash between two Sync calls loses
// at most the samples recorded since the last one — never corrupts what
// was already flushed, since each slot is a fixed-size record written in
// one memcpy.
func (r *Recorder) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return unix.Msync(r.data, unix.MS_SYNC)
}

// Close syncs and unmaps the file.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	syncErr := unix.Msync(r.data, unix.MS_SYNC)
	unmapErr := unix.Munmap(r.data)
	closeErr := r.f.Close()

	if syncErr != nil {
		return syncErr
	}
	if unmapErr != nil {
		return unmapErr
	}
	return closeErr
}
