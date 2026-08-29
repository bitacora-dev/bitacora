# internal/transport

The agent-to-hub wire protocol (ADR-0008): `POST /v1/ingest` over HTTP/2,
Protobuf body compressed with zstd.

- `server.go` — `Server.Handler()` / `Server.NewH2CServer()`. Auth →
  rate limit → decompress → unmarshal → host_id-matches-token check →
  idempotency check → hand off to `BatchReceiver` → respond with the
  confirmed offset.
- `client.go` — `Client.Send()`. Refuses cleartext HTTP to anything but
  loopback or a Tailscale-range address before making any network call.
- `token.go` / `authstore.go` — Argon2id token hashing and verification;
  `MemoryTokenStore` for tests and for a hub that hasn't wired real
  persistence yet.
- `idempotency.go` — `MemoryIdempotencyStore`; same caveat, not yet
  backed by real storage.
- `network.go` — `IsTrustedNetwork`, `ValidateTransportSecurity`,
  `ResolveBindAddr` (never `0.0.0.0` by default).
- `ratelimit.go` — `PerTokenLimiter`, one token bucket per host_id.

## What's NOT here yet

- **Persistence.** `BatchReceiver` is an interface the hub wires to real
  storage (`storage.Relational`, `internal/metricstore`,
  `internal/logstore`); nothing here assumes a backend. `MemoryTokenStore`
  and `MemoryIdempotencyStore` are test/scaffolding implementations, not
  what production runs — a hub restart forgets every token and every
  ingested batch ID.
- **Agent-side buffer/backfill** (ADR-0008's WAL, 2h/256MB, priority
  discard) — separate task.
- **`bita agent create`** — the tool ADR-0008 says generates and delivers
  a token. Not built; `MemoryTokenStore.AddToken` is the equivalent for
  tests today.
- **Immediate send on a `critical` event** (bypassing the 10s/1MB batch
  cadence) — that cadence logic lives in the agent's batching loop, which
  doesn't exist yet either.
