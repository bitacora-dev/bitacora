package blackbox

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// sampleEncodedSize is Sample's fixed on-disk size, computed once from the
// struct's own layout so it can never drift out of sync with encode/decode.
var sampleEncodedSize = binary.Size(Sample{})

func encodeSample(s Sample) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, sampleEncodedSize))
	// Every field in Sample is a fixed-size numeric type or a fixed-size
	// array of one — binary.Write never fails on that shape, so the error
	// is deliberately not checked here (open-coding it away would just
	// hide a mistake instead of handling a real failure mode).
	_ = binary.Write(buf, binary.LittleEndian, s)
	return buf.Bytes()
}

func decodeSample(raw []byte) (Sample, error) {
	if len(raw) != sampleEncodedSize {
		return Sample{}, fmt.Errorf("blackbox: record is %d bytes, expected %d", len(raw), sampleEncodedSize)
	}
	var s Sample
	if err := binary.Read(bytes.NewReader(raw), binary.LittleEndian, &s); err != nil {
		return Sample{}, fmt.Errorf("blackbox: decoding record: %w", err)
	}
	return s, nil
}
