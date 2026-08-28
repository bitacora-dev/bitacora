# ADR-0006: Esquema canónico de métricas, eventos y logs

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

La funcionalidad central del producto es la **línea temporal correlacionada**:
poder preguntar "¿qué pasaba a las 01:05 del martes?" y ver en un solo eje la
CPU, la temperatura, los mensajes del kernel, el estado de los servicios, los
contenedores y los jobs. Eso solo funciona si todo lo que entra al sistema
comparte un modelo temporal y de identidad común. Si cada collector inventa su
formato, la correlación es imposible o se convierte en código a medida por
fuente.

## Decisión

### Tres tipos de dato, tres modelos

#### 1. Métrica

Modelo Prometheus, sin desviaciones:

```
nombre{host_id="...", etiqueta="valor", ...} = valor_float64 @ timestamp_ms
```

Convenciones obligatorias:

- Prefijo `bitacora_` en todos los nombres.
- Unidades base del SI en el nombre: `_bytes`, `_seconds`, `_celsius`, `_ratio`
  (0-1, nunca porcentaje), `_total` para contadores monótonos.
- `host_id` presente **siempre**.
- Cardinalidad acotada. Prohibido usar como etiqueta: PID, ID completo de
  contenedor, ruta de fichero, dirección IP de cliente. Un ID de contenedor va
  truncado a 12 caracteres y acompañado de `container_name`.

Presupuesto de cardinalidad: **≤ 2000 series activas por host**. CI debe fallar
si un collector lo supera en el test de integración.

#### 2. Evento

Un hecho puntual, discreto y significativo. Es la pieza que hace posible la
correlación.

```json
{
  "id": "01J8XQ...",
  "ts": "2026-08-25T01:05:12.443Z",
  "host_id": "01J8X...",
  "source": "kernel",
  "type": "kernel.segfault",
  "severity": "error",
  "title": "segfault en node (cpu 8)",
  "subject": { "kind": "process", "name": "node", "pid": 44112 },
  "attrs": {
    "cpu": 8,
    "core_id": 4,
    "ip": "0x7f2a...",
    "sp": "0x7ffd...",
    "error_code": 4,
    "lib": "libnode.so.115"
  },
  "fingerprint": "sha256:...",
  "log_refs": [{ "block_id": "01J8...", "line": 8823 }],
  "schema": 1
}
```

- `type` es jerárquico y con espacio de nombres: `kernel.segfault`,
  `kernel.oom`, `raid.resync_started`, `job.failed`, `container.unhealthy`,
  `service.entered_failed`, `disk.smart_degraded`, `host.boot`,
  `agent.collector_disabled`.
- `severity`: `debug | info | notice | warn | error | critical`.
- `fingerprint`: hash estable de los campos identificativos, para deduplicar y
  agrupar recurrencias. Dos segfaults del mismo binario en la misma CPU
  comparten fingerprint; eso es lo que permite decir "34 veces en 6 días,
  siempre en CPU 8".
- `attrs` es JSON libre; se guarda como `JSONB` en PostgreSQL y como texto con
  índices sobre expresiones en SQLite. Solo se indexan las claves que se
  consultan.
- `log_refs` ancla el evento a las líneas de log que lo originaron, para poder
  saltar del evento al contexto crudo.

**Los eventos son el nivel de datos más valioso y el más pequeño. Nunca se
borran por presión de disco** (ADR-0003).

#### 3. Línea de log

```
ts | host_id | source | stream | unit_or_container | level | pid | message
```

Se almacena en bloques comprimidos (ADR-0003). No se convierte en evento salvo
que una **regla de extracción** lo promueva. Es la relación clave del sistema:

> Los logs son el material crudo. Los eventos son lo que se ha entendido de
> ellos. La UI navega de un lado al otro en ambos sentidos.

### Reglas de extracción

Declarativas, en YAML, incluidas en el paquete y ampliables por el usuario:

```yaml
- id: kernel-segfault
  source: kernel
  match: '(?P<comm>\S+)\[(?P<pid>\d+)\]: segfault at (?P<addr>\S+) ip (?P<ip>\S+) sp (?P<sp>\S+) error (?P<err>\d+)'
  emit:
    type: kernel.segfault
    severity: error
    enrich: [cpu_from_context, core_from_cpu]
```

`enrich: cpu_from_context` es específico de este proyecto: correlaciona el
segfault con la CPU lógica en la que ocurrió, cruzando con `/proc/<pid>/stat`
si el proceso aún existe o con el contexto del mensaje del kernel. Es lo que
convierte "hay segfaults" en "hay segfaults y todos caen en CPU 8".

### Tiempo

- **Todo en UTC**, `time.Time` con precisión de nanosegundos internamente,
  milisegundos al persistir.
- Se registran dos sellos: `ts` (cuándo ocurrió, según el host) y `ts_received`
  (cuándo llegó al hub). La UI usa `ts`; el diagnóstico de deriva usa la
  diferencia.
- Las zonas horarias son un asunto exclusivo de presentación.

### Versionado

Todo objeto persistido lleva `schema`. Los cambios compatibles (añadir campo
opcional) no lo incrementan; los incompatibles sí y requieren migración
explícita.

## Alternativas consideradas

- **OpenTelemetry como modelo de datos.** Tentador por interoperabilidad, y
  descartado para el MVP: el modelo de OTel está diseñado para trazas
  distribuidas, su SDK de Go es pesado para un agente que debe consumir 50 MB, y
  su modelo de logs es menos expresivo que un evento con `fingerprint` y
  `subject`. Se deja como **exportador opcional** en fase 4: emitir OTLP hacia
  fuera es fácil; construir sobre él, no.
- **Un único tipo de dato "observación" para todo.** Descartado: las métricas
  necesitan compresión numérica y los eventos necesitan estructura y búsqueda
  relacional. Unificarlos obliga a que uno de los dos vaya en un almacén
  inadecuado.
- **Guardar todas las líneas de log como eventos.** Descartado: dos órdenes de
  magnitud más de volumen, y ahoga la señal. Los eventos son la interpretación,
  no la materia prima.
- **Etiquetas libres sin límite de cardinalidad.** Descartado: es el fallo que
  hace explotar cualquier TSDB. El límite es una restricción de diseño, no una
  recomendación.

## Consecuencias

### Positivas
- La correlación temporal es una consulta, no una integración a medida por
  fuente.
- El `fingerprint` permite detectar patrones recurrentes automáticamente, que es
  exactamente lo que el caso de la CPU 8 requería y que hoy se hace a mano.
- Añadir un collector no requiere tocar el almacenamiento ni la UI: si emite
  métricas y eventos canónicos, aparece solo en la línea temporal.

### Negativas
- Rigidez: un collector que necesite un modelo distinto tendrá que forzarlo o
  requerir un ADR nuevo.
- El límite de cardinalidad restringe lo que se puede etiquetar y obliga a
  pensarlo por adelantado en cada collector.
- Las reglas de extracción con regex son frágiles ante cambios de formato del
  kernel o de las aplicaciones. Se mitigan con tests sobre corpus de log reales
  guardados en `testdata/`.

## Notas de implementación

- `testdata/logs/` debe contener extractos reales y anonimizados de journald,
  Docker, rclone y rsync. Cada regla de extracción tiene test con su corpus.
- Definir el catálogo inicial de `type` de evento en `docs/events.md` y
  mantenerlo como contrato público. Un `type` publicado no se renombra.
- Métricas mínimas obligatorias del propio agente:
  `bitacora_agent_cpu_seconds_total`, `bitacora_agent_memory_bytes`,
  `bitacora_agent_collector_duration_seconds`,
  `bitacora_agent_collector_errors_total`, `bitacora_agent_buffer_bytes`,
  `bitacora_agent_clock_offset_seconds`.
