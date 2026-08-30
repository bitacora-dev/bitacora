package blackbox

import (
	"fmt"
	"os"
)

// Dump reads every sample from a blackbox file at path, oldest first,
// without needing the agent or an mmap — ADR-0011: "el formato del
// fichero de caja negra debe ser legible sin el agente [...] sobre un
// fichero copiado desde otra máquina." A plain read is enough since the
// format is just a fixed header followed by fixed-size records.
//
// A ring that hasn't wrapped yet (WriteIndex < Capacity) has unwritten,
// all-zero slots past WriteIndex; Dump returns only the slots that have
// actually been written.
func Dump(path string) ([]Sample, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading blackbox file %s: %w", path, err)
	}

	h, err := decodeHeader(raw)
	if err != nil {
		return nil, err
	}

	written := h.WriteIndex
	if written > uint64(h.Capacity) {
		written = uint64(h.Capacity)
	}

	// Oldest-first order: if the ring has wrapped, the oldest surviving
	// sample is at slot WriteIndex % Capacity; if it hasn't, the oldest
	// is simply slot 0.
	var start uint64
	if h.WriteIndex > uint64(h.Capacity) {
		start = h.WriteIndex % uint64(h.Capacity)
	}

	samples := make([]Sample, 0, written)
	for i := uint64(0); i < written; i++ {
		slot := (start + i) % uint64(h.Capacity)
		offset := headerSize + int(slot)*int(h.SampleSize)
		if offset+int(h.SampleSize) > len(raw) {
			return samples, fmt.Errorf("blackbox file %s truncated at record %d", path, slot)
		}
		s, err := decodeSample(raw[offset : offset+int(h.SampleSize)])
		if err != nil {
			return samples, fmt.Errorf("blackbox file %s: %w", path, err)
		}
		samples = append(samples, s)
	}
	return samples, nil
}
