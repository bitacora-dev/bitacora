package agentbuffer

import (
	"fmt"
	"os"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// DiscardedItem records one item EnforceCapacity removed.
type DiscardedItem struct {
	Seq      uint64
	Priority Priority
	Kind     string // "log_line" or "metric" — never "event"
}

// EnforceCapacity discards buffered items — lowest priority, oldest
// first, and never an event — until the buffer is back within its
// age/byte budget (ADR-0008). Call this after Append; it's a no-op when
// already within budget.
func (b *Buffer) EnforceCapacity() ([]DiscardedItem, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var discarded []DiscardedItem

	for b.overCapacityLocked() {
		victim, ok := b.findDiscardVictimLocked()
		if !ok {
			// Only events remain. ADR-0008 says events are never
			// discarded — being over budget with nothing left to drop is
			// accepted, not an error.
			break
		}
		if err := b.removeItemLocked(victim.Seq); err != nil {
			return discarded, fmt.Errorf("discarding item %d: %w", victim.Seq, err)
		}
		discarded = append(discarded, DiscardedItem{Seq: victim.Seq, Priority: victim.Priority, Kind: kindOf(victim)})
	}

	return discarded, nil
}

func kindOf(it Item) string {
	switch {
	case it.LogLine != nil:
		return "log_line"
	case it.Metric != nil:
		return "metric"
	case it.Inventory != nil:
		return "inventory"
	default:
		return "event"
	}
}

func (b *Buffer) overCapacityLocked() bool {
	var totalBytes int64
	for _, seg := range b.sealed {
		totalBytes += seg.byteSize
	}
	if b.active != nil {
		totalBytes += int64(b.active.rawBytes)
	}
	if totalBytes > b.maxBytes {
		return true
	}

	oldest, ok := b.oldestTSLocked()
	if ok && time.Since(oldest) > b.maxAge {
		return true
	}
	return false
}

func (b *Buffer) oldestTSLocked() (time.Time, bool) {
	if len(b.sealed) > 0 {
		items, err := readSealedSegment(b.sealed[0].path)
		if err == nil && len(items) > 0 {
			return items[0].TS, true
		}
	}
	if b.active != nil && len(b.active.items) > 0 {
		return b.active.items[0].TS, true
	}
	return time.Time{}, false
}

func (b *Buffer) findDiscardVictimLocked() (Item, bool) {
	for _, prio := range []Priority{PriorityLogLine, PriorityMetric} {
		if best, ok := b.oldestWithPriorityLocked(prio); ok {
			return best, true
		}
	}
	return Item{}, false
}

func (b *Buffer) oldestWithPriorityLocked(prio Priority) (Item, bool) {
	var best Item
	found := false
	consider := func(it Item) {
		if it.Priority != prio {
			return
		}
		if !found || it.Seq < best.Seq {
			best = it
			found = true
		}
	}

	for _, seg := range b.sealed {
		items, err := readSealedSegment(seg.path)
		if err != nil {
			continue
		}
		for _, it := range items {
			consider(it)
		}
	}
	if b.active != nil {
		for _, it := range b.active.items {
			consider(it)
		}
	}

	return best, found
}

// removeItemLocked deletes exactly one item (by sequence number) from
// whichever segment holds it, rewriting that segment (or removing it
// entirely if it becomes empty).
func (b *Buffer) removeItemLocked(seq uint64) error {
	if b.active != nil {
		for i, it := range b.active.items {
			if it.Seq != seq {
				continue
			}
			b.active.items = append(b.active.items[:i], b.active.items[i+1:]...)
			if err := b.active.rewrite(); err != nil {
				return err
			}
			b.totalLen--
			return nil
		}
	}

	for i := range b.sealed {
		seg := &b.sealed[i]
		if seq < seg.minSeq || seq > seg.maxSeq {
			continue
		}
		items, err := readSealedSegment(seg.path)
		if err != nil {
			return err
		}

		found := false
		filtered := items[:0]
		for _, it := range items {
			if it.Seq == seq {
				found = true
				continue
			}
			filtered = append(filtered, it)
		}
		if !found {
			continue
		}

		if len(filtered) == 0 {
			if err := os.Remove(seg.path); err != nil {
				return err
			}
			b.sealed = append(b.sealed[:i], b.sealed[i+1:]...)
		} else {
			if err := writeSealedSegment(seg.path, filtered); err != nil {
				return err
			}
			seg.count = len(filtered)
			seg.minSeq = filtered[0].Seq
			seg.maxSeq = filtered[len(filtered)-1].Seq
			if info, err := os.Stat(seg.path); err == nil {
				seg.byteSize = info.Size()
			}
		}
		b.totalLen--
		return nil
	}

	return fmt.Errorf("item with seq %d not found in buffer", seq)
}

// BuildOverflowEvent builds the agent.buffer_overflow event ADR-0008 says
// must be emitted when the buffer discards data. The caller is
// responsible for delivering it (typically: Append it back into the same
// buffer as a normal event, so it's never lost even if the hub stays
// unreachable).
func BuildOverflowEvent(hostID string, discarded []DiscardedItem) schema.Event {
	counts := map[string]int{}
	for _, d := range discarded {
		counts[d.Kind]++
	}

	return schema.Event{
		ID:       ulid.Make().String(),
		TS:       time.Now().UTC(),
		HostID:   hostID,
		Source:   "agent",
		Type:     "agent.buffer_overflow",
		Severity: schema.SeverityWarn,
		Title:    fmt.Sprintf("buffer overflow: discarded %d item(s)", len(discarded)),
		Attrs: schema.Labels{
			"log_lines_discarded": fmt.Sprintf("%d", counts["log_line"]),
			"metrics_discarded":   fmt.Sprintf("%d", counts["metric"]),
		},
		Schema: schema.CurrentSchemaVersion,
	}
}
