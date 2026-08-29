package blackbox

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// magic identifies a blackbox file. Checked before trusting anything else
// in it — a copy of some unrelated file must never be silently decoded.
var magic = [8]byte{'B', 'T', 'C', 'R', 'B', 'L', 'K', 'B'}

// formatVersion is bumped on any incompatible change to header or record
// layout. ADR-0011 requires `bita blackbox dump` to keep working "incluso
// de otra versión" — only version 1 exists so far, so there's nothing to
// decode differently yet, but the version check below is what makes that
// possible later: a file from a version this build doesn't know fails
// with a clear, explicit error instead of being silently misdecoded as
// version 1's layout.
const formatVersion = 1

// headerSize is the fixed on-disk header size, in bytes. Padded well past
// the fields it currently holds so a future minor addition doesn't shift
// where records start.
const headerSize = 64

// header is the fixed preamble at the start of a blackbox file.
type header struct {
	Magic      [8]byte
	Version    uint32
	SampleSize uint32
	Capacity   uint32
	_          uint32 // alignment padding
	WriteIndex uint64 // total samples ever written; slot = WriteIndex % Capacity
}

func newHeader(capacity uint32) header {
	return header{
		Magic:      magic,
		Version:    formatVersion,
		SampleSize: uint32(sampleEncodedSize),
		Capacity:   capacity,
	}
}

func encodeHeader(h header) []byte {
	buf := make([]byte, headerSize)
	bw := bytes.NewBuffer(buf[:0])
	_ = binary.Write(bw, binary.LittleEndian, h)
	out := make([]byte, headerSize)
	copy(out, bw.Bytes())
	return out
}

func decodeHeader(raw []byte) (header, error) {
	if len(raw) < headerSize {
		return header{}, fmt.Errorf("blackbox: file too short for a header (%d bytes)", len(raw))
	}
	var h header
	if err := binary.Read(bytes.NewReader(raw[:headerSize]), binary.LittleEndian, &h); err != nil {
		return header{}, fmt.Errorf("blackbox: decoding header: %w", err)
	}
	if h.Magic != magic {
		return header{}, fmt.Errorf("blackbox: not a blackbox file (bad magic)")
	}
	if h.Version != formatVersion {
		return header{}, fmt.Errorf("blackbox: unsupported format version %d (this build reads %d)", h.Version, formatVersion)
	}
	return h, nil
}
