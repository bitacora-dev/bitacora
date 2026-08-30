# internal/collector/hwidentity

Physical-machine identity collector (ADR-0016): emits two Inventory kinds
per cycle, `hardware_identity` and `cpu_topology`. Everything it reads
degrades independently — a missing DMI file, no RAPL counter, or no
topology data each just leave that one attribute or item out, never fail
the whole collector.

- `hardware_identity` (one `system` item): `board_vendor`/`board_name`/
  `board_version`, `bios_vendor`/`bios_version`/`bios_date` from
  `/sys/class/dmi/id/*`; `cpu_model` from `/proc/cpuinfo`'s first "model
  name" line; `cpu_power_watts` from Intel RAPL
  (`/sys/class/powercap/intel-rapl:0/energy_uj`), computed as a delta
  between two timed samples — the first `Collect()` call only seeds the
  baseline and reports no wattage.
- `cpu_topology` (one item per logical CPU, ID `cpu<N>`): `core_id`,
  `online`, `core_type` (hybrid P-core/E-core awareness). This is a thin
  wrapper over `internal/faultcluster.ReadTopology`, reused as-is rather
  than reimplemented — that package already computes exactly this mapping
  for ADR-0011's fault-correlation work.

A VPS's virtual DMI table (e.g. `"QEMU"`, `"Google Compute Engine"`) is
reported as-is rather than detected and suppressed — that's itself useful
information about the host.

`Requires()` returns nil: no single capability gates this collector, since
every field degrades independently.

## What's NOT here

- RAPL access can be root-only on kernels patched for CVE-2020-8694; an
  unreadable counter yields "no power reading", not an error — there's no
  fallback power-measurement method (e.g. IPMI) for hosts without RAPL.
