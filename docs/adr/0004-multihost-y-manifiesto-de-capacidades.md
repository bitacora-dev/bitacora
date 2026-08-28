# ADR-0004: Multi-host y manifiesto de capacidades

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El parque inicial son cuatro máquinas profundamente heterogéneas:

| | iCloudServer | AlmaLinux | UnRaid | VPS OVH |
|---|---|---|---|---|
| Init | systemd | systemd | **rc.d, sin systemd** | systemd |
| Logs | journald | journald | **fichero syslog** | journald |
| Paquetes | apt | **dnf** | **plugins en `/boot`** | apt |
| SMART / hwmon | sí | sí | sí | **no (virtualizado)** |
| RAID | mdadm + SnapRAID | por determinar | **array propio de UnRaid** | no |
| MAC | AppArmor | **SELinux** | ninguno | AppArmor |
| Superficie | LAN + Tailscale | LAN | LAN | **internet público** |

Un collector escrito asumiendo journald y `apt` se rompe en dos de las cuatro.
La detección por nombre de host o por distribución (`if os == "unraid"`) esparce
condicionales por todo el código y hace imposible añadir una plataforma nueva
sin tocar el núcleo.

## Decisión

### 1. `host_id` en todo, desde el primer commit

Toda métrica, evento, log, job y alerta lleva `host_id` como dimensión de primer
nivel. No hay ningún camino de código que asuma un solo host, ni siquiera en la
fase mono-host.

`host_id` es un **ULID generado en la primera ejecución del agente** y persistido
en `/var/lib/bitacora/host_id`. No se deriva del hostname ni de la MAC ni del
`machine-id`: el hostname cambia, la MAC cambia y `machine-id` se clona al
duplicar una VM. El nombre legible (`hostname`) es un atributo mutable, no la
identidad.

### 2. Manifiesto de capacidades

Al arrancar, y cada vez que detecta un cambio, el agente ejecuta una **detección
de capacidades** y envía al hub un manifiesto declarativo:

```json
{
  "host_id": "01J8X...",
  "hostname": "icloudserver",
  "reported_at": "2026-08-28T10:00:00Z",
  "agent_version": "0.3.1",
  "os": { "family": "linux", "distro": "ubuntu", "version": "24.04", "kernel": "6.8.0-45-generic", "arch": "amd64" },
  "capabilities": {
    "init.systemd":        { "available": true,  "detail": "257" },
    "logs.journald":       { "available": true,  "detail": "persistent" },
    "logs.syslogfile":     { "available": false },
    "pkg.apt":             { "available": true },
    "pkg.dnf":             { "available": false },
    "storage.smart":       { "available": true,  "detail": "smartctl 7.4, 4 devices" },
    "storage.mdraid":      { "available": true,  "detail": "md0" },
    "storage.snapraid":    { "available": true,  "detail": "12.3" },
    "storage.mergerfs":    { "available": true },
    "storage.unraid_array":{ "available": false },
    "hw.hwmon":            { "available": true,  "detail": "coretemp, nvme" },
    "hw.edac":             { "available": true },
    "hw.rasdaemon":        { "available": false, "reason": "not installed" },
    "hw.pstore":           { "available": false, "reason": "ramoops not configured" },
    "container.docker":    { "available": true,  "detail": "27.1, swarm active" },
    "container.cgroupv2":  { "available": true },
    "net.tailscale":       { "available": true },
    "sec.selinux":         { "available": false },
    "sec.apparmor":        { "available": true },
    "public.exposed":      { "available": false }
  },
  "degraded": [
    { "capability": "hw.pstore", "impact": "no se podrá diagnosticar un cuelgue duro", "remedy": "docs/setup/ramoops.md" }
  ]
}
```

### 3. Los collectors declaran sus requisitos

Cada collector declara qué capacidades necesita y **se auto-desactiva** si no
están, emitiendo un evento informativo una sola vez. No hay comprobaciones de
plataforma dispersas por el código.

### 4. Los collectors heterogéneos se estructuran como interfaz + implementaciones

```
LogSource       → journald | syslogfile | dockerjson
PackageUpdates  → apt | dnf | unraid_plugins
ServiceManager  → systemd | rcd
RaidSource      → mdraid | unraid_array | zfs (futuro)
```

El núcleo solo conoce la interfaz. Añadir FreeBSD o un Synology es escribir una
implementación, no modificar el núcleo.

### 5. La UI se renderiza a partir del manifiesto

El VPS no muestra una pestaña de temperaturas vacía ni un "sin datos": **no
muestra la pestaña**. Lo que sí muestra es una sección de *capacidades
degradadas* con lo que podría estar midiendo y no mide, con enlace a la
documentación para habilitarlo.

## Alternativas consideradas

- **Detección por distribución** (`if distro == "almalinux"`). Descartado: los
  condicionales se multiplican, las combinaciones reales no coinciden con las
  distribuciones (un Ubuntu sin hwmon en una VM se comporta como el VPS) y cada
  plataforma nueva obliga a auditar todo el código.
- **Collectors que fallan silenciosamente si falta algo.** Descartado: produce
  el peor fallo posible, que es creer que se está midiendo algo que no se mide.
  El campo `degraded` existe precisamente para hacer visible ese vacío.
- **Una instancia de la aplicación por servidor, sin agregación.** Descartado:
  imposibilita la correlación entre hosts, que es donde está el valor real (un
  backup que sale de iCloudServer y aterriza en UnRaid es *un* job visto desde
  dos máquinas).
- **Dejar multi-host para una fase tardía.** Descartado: rehacer el modelo de
  datos y todos los collectors después cuesta 2-3× lo que cuesta diseñarlo así
  ahora.

## Consecuencias

### Positivas
- Añadir una plataforma es contenido, no cirugía.
- El usuario ve exactamente qué se está midiendo y qué no, y por qué.
- Las alertas pueden condicionarse a capacidades: una regla de temperatura no se
  evalúa en un host sin `hw.hwmon` en lugar de disparar por dato ausente.

### Negativas
- Sobrecoste estimado del **20-25 %** en esfuerzo total, concentrado en la
  disciplina de interfaces y en el soporte de UnRaid.
- El manifiesto es un contrato: cambiar el nombre de una capacidad rompe
  compatibilidad. Requiere versionado y una política de deprecación.
- Tentación de sobre-abstraer. Regla: una interfaz se crea cuando existe la
  **segunda** implementación real, no antes. Se admite una única excepción,
  `LogSource`, porque UnRaid está confirmado en el plan.

## Notas de implementación

### Orden de incorporación de hosts

1. **iCloudServer** — Ubuntu, el caso completo. Fase 1.
2. **AlmaLinux** — Fase 2. Es el que menos código nuevo requiere (systemd y
   journald iguales, solo cambia `dnf`), y por eso es el que valida si las
   abstracciones estaban bien planteadas. Requiere enviar una **política SELinux**
   junto al paquete RPM; en `enforcing` bloqueará el acceso a sockets y rutas.
3. **VPS OVH** — Fase 3. Aporta un collector que ninguna otra máquina necesita:
   **superficie pública** (intentos SSH fallidos, estado de fail2ban, reglas de
   firewall activas, consumo contra la cuota de tráfico de OVH).
4. **UnRaid** — Fase 3. La más costosa, y conviene estudiarla antes de
   comprometer fechas.

### Particularidades de UnRaid

Su raíz vive en `tmpfs` y se reconstruye en cada arranque: cualquier cosa
instalada en `/usr/local` desaparece al reiniciar. El agente debe instalarse en
`/boot/config/` (el pendrive) y lanzarse desde el script `go`, o empaquetarse
como plugin. Su array **no es mdadm estándar** y necesita un `RaidSource` propio
que lea el estado de emhttp. No hay systemd, luego el `ServiceManager` es `rcd`.

### Deriva de reloj

Con una máquina es irrelevante; con cuatro arruina la correlación temporal, que
es la funcionalidad central del producto. El hub registra para cada lote
`ts_agent` y `ts_received`, calcula el desfase por host y **alerta si supera 2 s**.
El estado de sincronización NTP es una métrica de primera clase.
