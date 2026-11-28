# hwmon collector

The `hwmon` collector emits `bitacora_cpu_temperature_celsius` every ten
seconds from Linux CPU thermal drivers (`coretemp`, `k10temp`, and `zenpower`).
It requires the `hw.hwmon` capability, so the runtime disables it visibly on a
host without hwmon devices instead of emitting invented zero values.

Each sample has the labels `chip` and `sensor`. They come from the driver
provided `name` and `tempN_label` files; `tempN` is used only when the driver
does not expose a label. Neither label includes the unstable `hwmonN` directory
number. Unreadable or absent inputs are omitted.

Only CPU-package and CPU-core devices are emitted. Disk, chipset, VRM, and PSU
sensors are excluded because this collector supplies the CPU panel, and their
unbounded device population would waste ADR-0006's per-host cardinality budget.
The collector has a hard limit of 128 CPU sensor series per host, selected by
the stable driver-provided identity, well below the 2,000-series budget.

The filesystem walk is implemented by `internal/hwmon` and is also used by the
ADR-0011 black box. The black box continues to read every temperature at 1 Hz;
this collector's 10-second schedule does not alter that independent sampler.
