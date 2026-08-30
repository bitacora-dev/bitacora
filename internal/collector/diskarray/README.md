# internal/collector/diskarray

Per-disk storage breakdown collector (ADR-0016): one `disk` Inventory item
per real mounted filesystem, instead of a single global usage percentage —
matching the "which physical disk is which, and how full is it"
information UnRaid-style panels show.

- `readRealMounts` parses `/proc/mounts` (device, mountpoint, fstype),
  decoding the kernel's octal `\NNN` escapes (e.g. `\040` for a space in a
  mountpoint) and filtering out pseudo/virtual filesystems (`proc`,
  `tmpfs`, `overlay`, ...) and anything not under `/dev/`.
- `statfsUsage` reads real capacity/used/available bytes via `statfs(2)`
  (`golang.org/x/sys/unix.Statfs_t`) against each mountpoint directly.
- `readSMARTIdentities` reads bitacora-smart's spool entry (ADR-0005) and
  joins each mount's `model`/`serial` by matching `baseDeviceName` (e.g.
  `/dev/sdc1` and `/dev/nvme0n1p1` both resolve to the whole-disk name
  `/sys/block` and bitacora-smart's `DeviceLister` use) — leniently
  parsing only the couple of fields this needs out of smartctl's much
  larger real JSON schema.

It doesn't try to know which disks belong to which named array (mdraid,
SnapRAID, UnRaid array) — each disk is reported independently, identified
by its own mountpoint and device.

`Requires()` returns nil: every real host has at least a root filesystem
to report, so no capability gate is needed.

## What's NOT here

- Array/pool membership (mdraid, ZFS pools, UnRaid's own array concept) —
  deliberately out of scope; this reports individual mounted filesystems,
  not how they're combined.
- SMART health/temperature data itself — that already lives in
  bitacora-smart's own metrics; this only borrows its spool for
  model/serial identity.
