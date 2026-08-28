# ADR-0010: Modelo `Job` y wrapper `bitacora-run`

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El requisito original enumeraba por separado: rclone, rsync, SnapRAID, backups
locales, backups a UnRaid, actualizaciones de Ubuntu. Y por otro lado pedía, para
cada uno: duración, ficheros transferidos, bytes transferidos, errores, última
ejecución correcta y próxima ejecución.

Son el mismo objeto. Tratarlos por separado significa escribir seis integraciones
a medida y seis vistas distintas para representar exactamente lo mismo.

Además, el enfoque de parsear logs a posteriori es intrínsecamente frágil: el
formato cambia entre versiones, un proceso matado por OOM no escribe la línea
final, y no hay forma fiable de saber el código de salida.

## Decisión

### Modelo canónico `Job`

```json
{
  "id": "01J8XR...",
  "job_name": "rclone-aginsur-sync",
  "host_id": "01J8X...",
  "started_at": "2026-08-28T02:00:00Z",
  "finished_at": "2026-08-28T02:41:33Z",
  "duration_seconds": 2493,
  "status": "success",
  "exit_code": 0,
  "stats": {
    "files_transferred": 1284,
    "bytes_transferred": 44230118400,
    "files_deleted": 12,
    "files_checked": 98221,
    "errors": 0
  },
  "peer_host_id": "01J8Y...",
  "trigger": "systemd-timer",
  "next_expected": "2026-08-29T02:00:00Z",
  "log_refs": [{ "block_id": "01J8...", "from": 0, "to": 8842 }],
  "schema": 1
}
```

- `status`: `running | success | warning | failed | timeout | killed`.
- `peer_host_id`: la otra máquina implicada, cuando la hay. Es lo que permite
  ver un backup de iCloudServer a UnRaid como **un solo job observado desde dos
  lados** en fase 4.
- `stats` es un mapa con claves canónicas cuando existen y libres cuando no.

### Instrumentación primaria: `bitacora-run`

Un wrapper que se antepone a cualquier comando:

```bash
bitacora-run --job rclone-aginsur-sync -- rclone sync /mnt/storage/aginsur remote:aginsur --use-json-log
```

Qué hace:

1. Registra inicio, con `trigger` detectado (systemd, cron, manual).
2. Ejecuta el comando, capturando `stdout` y `stderr` como líneas de log
   asociadas al job.
3. Captura el **código de salida real** y la señal recibida si la hubo.
4. Aplica un extractor de estadísticas según el tipo de comando detectado.
5. Escribe el `Job` al agente local (socket Unix), o al spool si el agente no
   está disponible.
6. Propaga el código de salida sin alterarlo.

Un job matado por OOM o por timeout queda registrado como `killed`, con su
señal. Eso es imposible de saber parseando logs.

### Extractores por tipo

| Comando | Fuente de estadísticas |
|---|---|
| rclone | `--use-json-log` (JSON estructurado, sin regex) |
| rsync | `--stats` sobre la salida |
| snapraid | parseo de `sync`/`scrub` |
| tar / restic / borg | extractores propios |
| genérico | duración, código de salida, líneas de salida |

Para rclone, adicionalmente: si el proceso corre con `--rc`, el agente consulta
la API de control para mostrar **progreso en vivo** (porcentaje, velocidad, ETA)
mientras el job está `running`.

### Instrumentación secundaria: parseo de logs existentes

Los logs históricos ya existentes (`/var/log/icloudserver/rsync/google-aginsur/`,
`/var/log/icloudserver/rclone/aginsur/`) se importan con un collector de
descubrimiento que aplica los mismos extractores sobre ficheros ya escritos. Es
el camino de compatibilidad, no el recomendado, y así debe estar documentado.

### Descubrimiento y deadman automático

El sistema observa la cadencia de cada `job_name`. Tras tres ejecuciones con
periodicidad regular, **propone** una regla deadman (ADR-0009) que el usuario
confirma con un clic. Esto ataca el fallo previsible de que nadie configure a
mano las reglas de ausencia.

También lee los `OnCalendar` de los timers de systemd para rellenar
`next_expected` sin adivinar.

## Alternativas consideradas

- **Parseo de logs como mecanismo principal.** Descartado como estrategia
  primaria: frágil ante cambios de formato, incapaz de detectar procesos muertos
  abruptamente, y sin acceso al código de salida. Se mantiene como camino de
  compatibilidad para lo que ya existe.
- **Un collector específico por herramienta** (collector de rclone, collector de
  rsync, collector de SnapRAID). Descartado: duplica el modelo y la UI para el
  mismo concepto. Los extractores dentro de un modelo único dan lo mismo con una
  fracción del código.
- **Integración vía systemd solamente** (`systemd-run` y estado de unidad).
  Descartado: no funciona en UnRaid, no cubre cron ni ejecuciones manuales, y no
  da estadísticas de transferencia.
- **Que `bitacora-run` escriba directamente al hub.** Descartado: obligaría a
  distribuir el token de ingesta a cualquier script y a manejar reintentos de
  red en un wrapper. Escribe al agente local, que ya tiene buffer y credencial.

## Consecuencias

### Positivas
- Una sola vista, un solo modelo y una sola lógica de alertas para todo lo
  periódico: backups, sincronizaciones, scrubs y actualizaciones.
- `bitacora-run` es una pieza **atractiva por sí sola para terceros**: se puede
  anteponer a cualquier cron y obtener observabilidad de golpe. Es probablemente
  el mejor gancho de adopción del proyecto.
- Detecta procesos matados por OOM o timeout, que es imposible por log.

### Negativas
- Requiere **modificar los crontabs y las unidades systemd existentes** para
  anteponer el wrapper. Es fricción real de migración y hay que documentarla
  bien.
- El wrapper se convierte en un punto de fallo: si `bitacora-run` falla, no debe
  impedir que el backup se ejecute. Requisito duro: **ante cualquier error
  interno, ejecuta el comando igualmente** y registra el fallo de
  instrumentación aparte.
- Los extractores por herramienta requieren mantenimiento cuando cambian los
  formatos de salida.

## Notas de implementación

- `bitacora-run` debe ser un binario **muy pequeño y sin dependencias**, con
  arranque de milisegundos. No comparte código pesado con el agente.
- Test obligatorio: matar el proceso hijo con `SIGKILL` a mitad y verificar que
  el job queda registrado como `killed` con la señal correcta.
- Test obligatorio: con el agente parado, el comando se ejecuta igual y el job
  aparece cuando el agente vuelve.
- Timeout configurable por job, con `SIGTERM` seguido de `SIGKILL` tras un
  margen de gracia.
- La UI debe mostrar los jobs en curso con su progreso, no solo el histórico.
