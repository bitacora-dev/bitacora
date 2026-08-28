# ADR-0005: Modelo de privilegios y helpers

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

Un agente de observabilidad necesita datos que en Linux están repartidos entre
tres niveles: legibles por cualquiera (`/proc`, `/sys`), legibles por un grupo
(journald), y exclusivos de root (SMART, `mdadm --detail`, SnapRAID).

La salida fácil es correr el agente como root. Es también la peor: un proceso
root permanente, con acceso a red, parseando entrada no confiable (nombres de
contenedor, líneas de log, salida de comandos) en cuatro máquinas, una de ellas
expuesta a internet.

## Decisión

### Principio

**El demonio principal no corre nunca como root.** Corre como usuario de sistema
`bitacora`, sin shell, sin home. Lo que requiere privilegios se aísla en
*helpers*: procesos de vida corta, lanzados por un timer de systemd, que ejecutan
una tarea acotada, escriben JSON a un directorio de intercambio y mueren.

### Matriz de acceso

| Fuente | Mecanismo | Privilegio |
|---|---|---|
| `/proc`, `/sys`, `/sys/class/hwmon` | lectura directa | ninguno |
| `/proc/mdstat` | lectura directa | ninguno |
| `/sys/fs/cgroup/**` | lectura directa | ninguno |
| journald | `sdjournal` con cursor persistido | grupo `systemd-journal` |
| systemd (listar unidades, estado) | D-Bus, métodos de solo lectura | ninguno |
| Docker: CPU/RAM/red por contenedor | **cgroup v2 directamente** | ninguno |
| Docker: metadatos, healthcheck, eventos | `docker-socket-proxy` (solo GET) | ninguno sobre el socket real |
| SMART | helper `bitacora-smart` | root, timer 15 min |
| `mdadm --detail` | helper `bitacora-mdadm` | root, timer 5 min |
| SnapRAID `status` | helper `bitacora-snapraid` | root, timer 1 h |
| `/sys/fs/pstore` | helper `bitacora-pstore` | root, una vez al arrancar |

### Docker sin el grupo `docker`

**No se añade el usuario `bitacora` al grupo `docker`.** Pertenecer a ese grupo
es equivalente a ser root: con acceso al socket se puede arrancar un contenedor
privilegiado que monte `/` del host. Es una escalada trivial y documentada.

En su lugar:

1. **Las métricas de recursos por contenedor se leen de cgroup v2**
   (`/sys/fs/cgroup/system.slice/docker-<id>.scope/`). No requiere permiso
   alguno, y además es más barato que consultar la API: sin serialización, sin
   round-trip, sin carga sobre el demonio de Docker.
2. **Los metadatos** (nombres, imagen, estado de healthcheck, etiquetas, stream
   de eventos) se obtienen a través de **`docker-socket-proxy`**, configurado con
   solo lectura sobre `/containers`, `/info`, `/events`, `/version`, y todo lo
   demás denegado.

Si el proxy no está desplegado, el collector de Docker funciona en modo
degradado: métricas de recursos sí, metadatos no. Se refleja en el manifiesto de
capacidades (ADR-0004) con la remediación correspondiente.

### Directorio de intercambio

`/var/lib/bitacora/spool/`, propiedad de `root:bitacora`, permisos `0750`.
Los helpers escriben; el agente lee y borra. Escritura atómica: fichero temporal
más `rename(2)`.

Formato de cada fichero: `{ "collector": "smart", "ts": "...", "schema": 1, "data": {...}, "errors": [...] }`.

El agente **valida el esquema y la antigüedad** de cada fichero. Un fichero con
más de tres intervalos de antigüedad se descarta y genera un evento: un helper
que ha dejado de ejecutarse debe ser visible, no invisible.

### Blindaje de las unidades systemd

Demonio principal (`bitacora-agent.service`):

```ini
User=bitacora
Group=bitacora
SupplementaryGroups=systemd-journal
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=no
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallFilter=@system-service
SystemCallArchitectures=native
CapabilityBoundingSet=
ReadWritePaths=/var/lib/bitacora
```

Helpers (`bitacora-smart.service`, tipo `oneshot`):

```ini
User=root
Type=oneshot
NoNewPrivileges=yes
ProtectSystem=strict
PrivateNetwork=yes
CapabilityBoundingSet=CAP_SYS_RAWIO CAP_SYS_ADMIN
ReadWritePaths=/var/lib/bitacora/spool
RuntimeMaxSec=60
```

`PrivateNetwork=yes` en los helpers es importante: un proceso root que no
necesita red no debe tenerla.

## Alternativas consideradas

- **Agente como root.** Descartado. Es la fuente de la mayoría de los CVE de
  agentes de monitorización.
- **Capacidades sobre el binario principal** (`CAP_SYS_RAWIO` vía
  `AmbientCapabilities`). Descartado: la capacidad estaría presente durante toda
  la vida del proceso, incluido el tiempo en que parsea entrada no confiable y
  habla por red. Los helpers la tienen durante milisegundos y sin red.
- **`sudo` con reglas específicas.** Descartado: depende de configuración manual
  fuera del paquete, es frágil ante actualizaciones y difícil de auditar.
- **Un único helper monolítico que lo haga todo.** Descartado: cada helper tiene
  su propia cadencia (SMART cada 15 min, SnapRAID cada hora) y su propio conjunto
  mínimo de capacidades. Fusionarlos obliga a conceder la unión de todos los
  permisos durante todo el tiempo.
- **Polkit para elevar puntualmente.** Descartado para el MVP: añade una
  dependencia que no existe en UnRaid y complica el empaquetado.

## Consecuencias

### Positivas
- Comprometer el agente no da root. La superficie root total del sistema es de
  unos pocos segundos por hora, sin red y con `CapabilityBoundingSet` mínimo.
- Las métricas de contenedor por cgroup son más baratas que vía API.
- El modelo es auditable: cualquiera puede leer las unidades systemd y ver
  exactamente qué se ejecuta con privilegios.

### Negativas
- Los datos de los helpers **no son en tiempo real**: SMART tiene hasta 15
  minutos de retraso. Es aceptable (SMART cambia en horas), pero debe mostrarse
  la antigüedad del dato en la UI, nunca presentarlo como instantáneo.
- Más unidades systemd que instalar y explicar.
- `docker-socket-proxy` es una pieza adicional a desplegar, y sin ella el
  collector de Docker queda degradado.
- El modelo de helpers no se traslada a UnRaid, que no tiene systemd: allí hará
  falta un planificador propio (cron o bucle en el script `go`).

## Notas de implementación

- `bita doctor` debe verificar y reportar: pertenencia a grupos, permisos
  del spool, presencia de los timers, accesibilidad del proxy de Docker y
  frescura de cada fichero de spool. Es la primera herramienta que debe existir
  después del esqueleto.
- Todo dato procedente de un helper o de un log se trata como **entrada no
  confiable**: nombres de contenedor, rutas y mensajes de log pueden contener
  secuencias de escape o intentos de inyección. Se sanean antes de indexar y
  antes de renderizar.
- El agente **nunca** ejecuta comandos derivados de datos observados. Ver
  ADR-0012.
