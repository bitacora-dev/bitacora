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
- Array membership is read from `/proc/mdstat` for mdraid and from
  `snapraid.conf` for SnapRAID. Matching disk items gain `array_type`,
  `array_level`, `array_member_count`, and `array_health`; disks outside an
  array gain none of these attributes. mdraid health is `healthy` or
  `degraded` from the kernel status. SnapRAID health is `unknown`, because
  determining it would require running SnapRAID, which ADR-0012 forbids.

`Requires()` returns nil: every real host has at least a root filesystem
to report, so no capability gate is needed.

## What's NOT here

- ZFS pools and UnRaid's own array concept. UnRaid remains out of scope until
  its read-only `/proc/mdcmd` format is documented and can be mapped to the
  mounted devices without invoking `mdcmd`; its capability is still detected
  by `internal/capabilities`.
- SMART health/temperature data itself — that already lives in
  bitacora-smart's own metrics; this only borrows its spool for
  model/serial identity.
