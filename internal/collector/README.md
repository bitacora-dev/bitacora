# internal/collector

The `Collector` contract and its runtime (ADR-0007). Read `example/` before
writing a new collector — it's the reference implementation with all the
correct patterns.

- `collector.go` — the `Collector` and `Sink` interfaces, and their
  supporting types.
- `clock.go` — the injected `Clock`/`Ticker`, so the runtime is testable
  without real sleeps.
- `runtime.go` — one goroutine per collector, hard timeout, panic recovery,
  exponential backoff.
- `registry.go` — registers collectors and resolves which ones can run given
  the host's available capabilities (ADR-0004).
- `example/` — canonical reference collector.

Rules a new collector must follow (enforced by the runtime, not by
convention): respect context cancellation in `Collect`, never call
`time.Sleep`, never touch storage or the network directly, keep no mutable
package-level state.
