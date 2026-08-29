# docker collector

Per-container CPU and memory from cgroup v2. Container names from
docker-socket-proxy, best-effort (ADR-0005).

## Metrics

| Name | Labels | Unit |
|---|---|---|
| `bitacora_container_cpu_seconds_total` | `container_id` (12 chars), `container_name` | seconds, counter |
| `bitacora_container_memory_bytes` | `container_id`, `container_name` | bytes |

Without `docker_socket_proxy_url` configured, `container_name` falls back
to the truncated `container_id` — schema validation requires the pair
together, and metrics still flow (ADR-0005's degraded mode), just without
a human-readable name.

## Capability

`container.cgroupv2` (hard requirement). `container.docker` (the
socket-proxy) is not gated on — its absence degrades labels, never
disables the collector.

## Events

None.

## Testdata

`testdata/cgroup/docker-<id>.scope/{cpu.stat,memory.current}` are
synthetic, in the real cgroup v2 file format, not captures from a real
Docker host (none was available while writing this).
