package logstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VerifyResult summarizes an index reconstruction: what was found, and
// anything that looks wrong. A well-formed store has zero orphans and zero
// corrupt entries — ADR-0003's whole point is that this scan alone is
// enough to trust the store even if the relational index is gone.
type VerifyResult struct {
	Blocks         []BlockMeta
	TotalRawBytes  int64
	TotalCompBytes int64

	// OrphanPayloads are .zst files with no matching .meta.json sidecar.
	OrphanPayloads []string
	// OrphanMeta are .meta.json sidecars with no matching .zst payload.
	OrphanMeta []string
	// Corrupt are .meta.json files that failed to parse, with the reason.
	Corrupt map[string]string
}

// ScanIndex rebuilds the block index by walking baseDir for
// <ulid>.meta.json sidecars, with no dependency on the relational
// database — the reconstruction ADR-0003 requires and `bita logs verify`
// exposes.
func ScanIndex(baseDir string) (VerifyResult, error) {
	result := VerifyResult{Corrupt: map[string]string{}}

	metaFiles := map[string]string{}    // block id -> absolute .meta.json path
	payloadFiles := map[string]string{} // block id -> absolute .zst path

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".meta.json"):
			id := strings.TrimSuffix(filepath.Base(path), ".meta.json")
			metaFiles[id] = path
		case strings.HasSuffix(path, ".zst"):
			id := strings.TrimSuffix(filepath.Base(path), ".zst")
			payloadFiles[id] = path
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("scanning %s: %w", baseDir, err)
	}

	for id, metaPath := range metaFiles {
		if _, ok := payloadFiles[id]; !ok {
			result.OrphanMeta = append(result.OrphanMeta, metaPath)
			continue
		}

		raw, err := os.ReadFile(metaPath)
		if err != nil {
			result.Corrupt[metaPath] = err.Error()
			continue
		}
		var meta BlockMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			result.Corrupt[metaPath] = err.Error()
			continue
		}

		result.Blocks = append(result.Blocks, meta)
		result.TotalRawBytes += meta.SizeRaw
		result.TotalCompBytes += meta.SizeCompressed
	}

	for id, payloadPath := range payloadFiles {
		if _, ok := metaFiles[id]; !ok {
			result.OrphanPayloads = append(result.OrphanPayloads, payloadPath)
		}
	}

	return result, nil
}
