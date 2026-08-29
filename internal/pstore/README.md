# internal/pstore

Ingests `/sys/fs/pstore` (ADR-0011): the kernel's own reserved,
reboot-surviving RAM region for an oops or panic dump. The one mechanism
that would have captured the cause of the incident this ADR is named
after, had there been a silent oops instead of a truly hard hang.

- `List` reads every `dmesg-*` entry (pstore's other record types —
  `console-*`, `pmsg-*`, `ftrace-*` — aren't "el oops o el panic" this
  ADR wants).
- `ToEvent` converts one entry into a `kernel.crash_dump` Event, with a
  bounded excerpt of the dump content as an attribute.
- `Consume` does both, then removes the file — ADR-0011: "limpia la
  región." pstore's backing RAM is small and finite; it has to be freed
  for the next crash to have somewhere to go, regardless of what happens
  to the content afterward.

See `docs/setup/ramoops.md` for how to get `/sys/fs/pstore` populated in
the first place — many UEFI systems already have a working backend
(`efi_pstore`) with zero configuration; others need `ramoops.*` kernel
parameters.

## What's NOT here

- **The full dump archived somewhere durable.** `MaxExcerptBytes` (4000)
  rides along on the Event as an attribute — enough to see what
  happened without leaving the timeline, but not the complete dump. A
  proper archive would mean writing the raw content through
  `internal/logstore` and attaching a `LogRef` instead of an inline
  excerpt; that integration isn't built here.
- **Calling `Consume` from the agent's startup path.** This package is a
  library, tested standalone against a fixture pstore directory — nothing
  currently calls it when `cmd/bitacora-agent` starts.
