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
  joins each mount's `model`, `serial`, `temperature_celsius` and
  `smart_status` by matching `baseDeviceName` (e.g. `/dev/sdc1` and
  `/dev/nvme0n1p1` both resolve to the whole-disk name `/sys/block` and
  bitacora-smart's `DeviceLister` use) — leniently parsing only the few
  fields this needs out of smartctl's much larger real JSON schema.
  `smart_status` is `passed` or `failed`, straight from smartctl's overall
  verdict. Both attributes are omitted when the device reports no
  temperature sensor or no overall verdict: ADR-0016 wants the real value
  or nothing, never a 0 °C that reads like a cold disk.
- Array membership is read from `/proc/mdstat` for mdraid and from
  `snapraid.conf` for SnapRAID. Matching disk items gain `array_type`,
  `array_level`, `array_member_count`, and `array_health`; disks outside an
  array gain none of these attributes. mdraid health is `healthy` or
  `degraded` from the kernel status. SnapRAID health is `unknown`, because
  determining it would require running SnapRAID, which ADR-0012 forbids.
  SnapRAID members also gain `array_role` (`parity` or `data`) from the
  keyword their location was declared with, which is what lets a panel
  group parity and data disks apart.

`Requires()` returns nil: every real host has at least a root filesystem
to report, so no capability gate is needed.

## What's NOT here

- ZFS pools and UnRaid's own array concept. UnRaid remains out of scope until
  its read-only `/proc/mdcmd` format is documented and can be mapped to the
  mounted devices without invoking `mdcmd`; its capability is still detected
  by `internal/capabilities`.
- Collecting SMART data. This collector never runs `smartctl`; it only
  reads the spool entry bitacora-smart already wrote (ADR-0005), so a host
  without that helper reports mounts, usage and array topology with no
  identity, temperature or health at all.
- Spin state (UnRaid's "active" / "standby" light). `smartctl --json -a`
  does not report a power mode, and asking for one is a different helper
  invocation; nothing here infers it.
