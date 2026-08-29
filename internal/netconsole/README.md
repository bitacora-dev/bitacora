# internal/netconsole

The hub-side receiver for the kernel's own netconsole (ADR-0011): a live
UDP stream of `printk` lines sent to another machine, so a host that
hard-hangs before it can flush anything to its own disk still has its
last kernel messages somewhere safe.

- `Server.Serve` listens on a `*net.UDPConn` and decodes each datagram
  into a `schema.LogLine{Source: "kernel_remote", ...}`, handed to a
  `Receiver` — same pluggable-sink shape as `transport.BatchReceiver` and
  `job.Receiver`.
- Decodes both netconsole wire formats: the basic `<level>message` and
  the extended `PRI,SEQ,TS,FLAGS;message` (Linux's own
  `Documentation/networking/netconsole.rst`). A packet that matches
  neither is still delivered as a plain message with no resolved level,
  rather than dropped — the whole point of netconsole is capturing
  whatever made it out, not being picky about it.
- A malformed or empty packet is silently dropped, not fatal to the
  server — same reasoning.

See `docs/setup/netconsole.md` for enabling it on a source host (configfs
or module parameter) and opening the receiving port on the hub.

## host_id resolution

A UDP packet only carries a source IP:port — there's no host_id in the
protocol itself. `Server.Resolver` is an optional
`func(*net.UDPAddr) string` the hub can supply (e.g. backed by its own
known-agents table); when it's nil, or returns `""`, the source IP string
is used as `HostID` directly. A real per-deployment IP→host_id registry
doesn't exist yet — this is the honest, documented fallback rather than a
silent guess.

## What's NOT here

- **Actually starting a listener from `cmd/bitacora-hub`.** This package
  is tested against a real `*net.UDPConn` in-process; nothing in the hub
  binary opens one yet.
- **A `Resolver` backed by the hub's real agent registry.** The interface
  exists specifically so the hub can plug one in later without touching
  this package.
