# cpu collector

Reads `/proc/stat` via `procfs`. Requires no capability — always readable
without privilege.

## Metrics

| Name | Labels | Unit |
|---|---|---|
| `bitacora_cpu_usage_ratio` | `cpu` (`"total"` or a core index, e.g. `"0"`) | ratio, 0-1 |

Nothing is emitted on the first `Collect` call: usage is a delta between two
cumulative samples, so there's no baseline yet.

## Events

None.

## Testdata

`testdata/procfs/stat` is a synthetic fixture in the real `/proc/stat`
format (two cores), not a capture from a real machine — none was available
while writing this. Swap it for a real capture from iCloudServer once one
exists, per ADR-0007's fixture requirement.
