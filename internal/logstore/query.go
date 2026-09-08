package logstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxQueryRange bounds durable-block reads. The store has no global text
// index, so callers must keep searches narrow enough to inspect only the
// relevant host/day directories and candidate compressed blocks.
const MaxQueryRange = 31 * 24 * time.Hour

// Query describes an explicit, bounded durable-log search. Source and Unit
// are applied from block metadata before opening a compressed payload; Text is
// applied only after that candidate reduction.
type Query struct {
	HostID string
	From   time.Time
	To     time.Time
	Text   string
	Source string
	Unit   string
	Limit  int
	Offset int
}

// Entry is the highest-fidelity representation available from existing blocks.
// Older block payloads store newline-delimited messages only, so TS is the
// block's ts_min and Unit is block-level metadata rather than per-line data.
type Entry struct {
	ID      string    `json:"id"`
	TS      time.Time `json:"ts"`
	HostID  string    `json:"host_id"`
	Source  string    `json:"source"`
	Unit    string    `json:"unit,omitempty"`
	Message string    `json:"message"`
}

type Page struct {
	Entries []Entry `json:"entries"`
	Total   int     `json:"total"`
}

// Query reads only metadata sidecars beneath the requested host's bounded
// UTC-day directories. Payloads are decompressed only for overlapping blocks
// that already match source/unit metadata, avoiding an indiscriminate scan of
// every host or payload in the durable store.
func (s *Store) Query(ctx context.Context, q Query) (Page, error) {
	if q.HostID == "" || q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) {
		return Page{}, fmt.Errorf("host_id and an ordered time range are required")
	}
	if q.To.Sub(q.From) > MaxQueryRange {
		return Page{}, fmt.Errorf("time range exceeds %s", MaxQueryRange)
	}
	if q.Limit < 1 || q.Limit > 500 || q.Offset < 0 {
		return Page{}, fmt.Errorf("invalid pagination")
	}

	metas, err := s.candidateMetas(ctx, q)
	if err != nil {
		return Page{}, err
	}
	var all []Entry
	needle := strings.ToLower(q.Text)
	for _, meta := range metas {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		raw, err := DecompressBlock(filepath.Join(s.baseDir, meta.Path))
		if err != nil {
			return Page{}, fmt.Errorf("decompressing block %s: %w", meta.BlockID, err)
		}
		for lineNo, message := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
			if needle != "" && !strings.Contains(strings.ToLower(message), needle) {
				continue
			}
			all = append(all, Entry{ID: fmt.Sprintf("%s:%d", meta.BlockID, lineNo), TS: meta.TSMin, HostID: meta.HostID, Source: meta.Source, Unit: meta.Unit, Message: message})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].TS.After(all[j].TS) })
	total := len(all)
	if q.Offset >= total {
		return Page{Entries: []Entry{}, Total: total}, nil
	}
	end := q.Offset + q.Limit
	if end > total {
		end = total
	}
	return Page{Entries: all[q.Offset:end], Total: total}, nil
}

func (s *Store) candidateMetas(ctx context.Context, q Query) ([]BlockMeta, error) {
	var metas []BlockMeta
	// A block can start shortly before midnight and extend into the range.
	for day := q.From.UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour); !day.After(q.To.UTC()); day = day.AddDate(0, 0, 1) {
		dir := filepath.Join(s.baseDir, q.HostID, fmt.Sprintf("%04d", day.Year()), fmt.Sprintf("%02d", day.Month()), fmt.Sprintf("%02d", day.Day()))
		files, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading log metadata: %w", err)
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".meta.json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
			if err != nil {
				return nil, fmt.Errorf("reading metadata: %w", err)
			}
			var meta BlockMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				return nil, fmt.Errorf("parsing metadata: %w", err)
			}
			if meta.HostID != q.HostID || meta.TSMax.Before(q.From) || meta.TSMin.After(q.To) || (q.Source != "" && meta.Source != q.Source) || (q.Unit != "" && meta.Unit != q.Unit) {
				continue
			}
			metas = append(metas, meta)
		}
	}
	return metas, nil
}
