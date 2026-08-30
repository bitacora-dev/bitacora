# memory collector

Reads `/proc/meminfo` via `procfs`. Requires no capability — always
readable without privilege.

## Metrics

| Name | Labels | Unit |
|---|---|---|
| `bitacora_memory_total_bytes` | none | bytes |
| `bitacora_memory_available_bytes` | none | bytes |
| `bitacora_memory_used_ratio` | none | ratio, 0-1 — `(total - available) / total` |
| `bitacora_memory_swap_total_bytes` | none | bytes |
| `bitacora_memory_swap_free_bytes` | none | bytes |

## Events

None.

## Testdata

`testdata/procfs/meminfo` is a synthetic fixture in the real
`/proc/meminfo` format, not a capture from a real machine — none was
available while writing this. Swap it for a real capture from iCloudServer
once one exists, per ADR-0007's fixture requirement.
