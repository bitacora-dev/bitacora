package job

import (
	"context"
	"fmt"
	"time"
)

// DefaultDialTimeout bounds how long bitacora-run waits for the agent
// socket before falling back to the spool. It must stay short: a stalled
// agent must never make the wrapped command's caller (cron, systemd) wait.
const DefaultDialTimeout = 2 * time.Second

// DeliveryPath reports which path a Report actually used.
type DeliveryPath string

const (
	DeliveredViaSocket DeliveryPath = "socket"
	DeliveredViaSpool  DeliveryPath = "spool"
)

// Report delivers j to the agent at socketPath; if that fails for any
// reason (agent down, socket missing, timeout), it falls back to writing j
// into spoolDir (ADR-0010: "al agente local [...], o al spool si el agente
// no está disponible"). It only returns an error when both paths fail —
// losing the job outright, which the spool is specifically meant to
// prevent.
func Report(ctx context.Context, socketPath, spoolDir string, j Job, dialTimeout time.Duration) (DeliveryPath, error) {
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	if err := Deliver(dialCtx, socketPath, j); err == nil {
		return DeliveredViaSocket, nil
	}

	if err := WriteSpool(spoolDir, j); err != nil {
		return "", fmt.Errorf("job %s: agent unreachable and spool write failed: %w", j.ID, err)
	}
	return DeliveredViaSpool, nil
}

// Backfill drains spoolDir into the agent at socketPath, oldest first,
// stopping at the first delivery failure and returning how many jobs made
// it through — the same "resume where it left off" contract as
// agentbuffer.Backfill. A job is removed from the spool only after the
// agent has acknowledged it.
func Backfill(ctx context.Context, socketPath, spoolDir string, dialTimeout time.Duration) (int, error) {
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}

	spooled, err := ReadSpool(spoolDir)
	if err != nil {
		return 0, fmt.Errorf("reading job spool %s: %w", spoolDir, err)
	}

	sent := 0
	for _, sj := range spooled {
		if err := ctx.Err(); err != nil {
			return sent, err
		}

		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		err := Deliver(dialCtx, socketPath, sj.Job)
		cancel()
		if err != nil {
			return sent, fmt.Errorf("delivering spooled job %s: %w", sj.Job.ID, err)
		}

		if err := RemoveSpool(sj.Path); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
