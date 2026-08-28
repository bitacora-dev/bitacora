# ADR-0009: Motor de alertas y notificación

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

Un sistema de alertas mal diseñado produce fatiga en una semana y a partir de
ahí nadie mira los avisos, con lo que el sistema es peor que no tener nada.

Además, el requisito original contemplaba "backup fallido → alerta", pero omitía
el caso más peligroso: **el backup que no se ejecuta**. Un fallo es ruidoso; una
ausencia es silenciosa. Los backups se caen en silencio, no con estruendo.

## Decisión

### Máquina de estados

Toda alerta recorre estados explícitos:

```
inactive → pending → firing → resolved
              ↓
          (se cancela si la condición desaparece antes de `for`)
```

- **`for`**: una condición debe mantenerse durante un tiempo antes de disparar.
  Elimina el ruido de los picos transitorios.
- **Histéresis**: umbral de disparo y umbral de resolución distintos (dispara a
  85 °C, resuelve a 75 °C). Sin esto, una temperatura oscilando en el umbral
  genera decenas de notificaciones.
- **Deduplicación** por `fingerprint` de la regla más las etiquetas.
- **Silencios y ventanas de mantenimiento**: por host, por regla o por etiqueta,
  con caducidad obligatoria. Un silencio sin fecha de fin no se permite.
- **Agrupación**: alertas del mismo host en una ventana corta se notifican
  juntas, no una a una.
- **Historial completo**: cada transición de estado se persiste. "Este disco ha
  alertado 4 veces en 2 meses" es información diagnóstica de primer orden.

### Tipos de regla

#### 1. Umbral sobre métrica

```yaml
- id: cpu-temp-alta
  expr: bitacora_cpu_temperature_celsius > 85
  resolve_expr: bitacora_cpu_temperature_celsius < 75
  for: 5m
  severity: warn
  requires: [hw.hwmon]
```

`requires` es esencial: sin él, la regla se evaluaría en el VPS (que no tiene
sensores) y dispararía o fallaría por dato ausente.

#### 2. Coincidencia de evento

```yaml
- id: segfault-recurrente
  on_event: kernel.segfault
  group_by: [attrs.cpu, subject.name]
  threshold: { count: 3, window: 24h }
  severity: error
```

Esta regla concreta habría detectado sola el patrón de la CPU 8.

#### 3. Deadman (ausencia)

**El tipo de regla más importante del sistema.**

```yaml
- id: backup-aginsur-ausente
  deadman:
    job: rclone-aginsur-sync
    expect_every: 24h
    grace: 6h
  severity: critical
```

Semántica: "esperaba una ejecución cada 24 h y llevo 31 h sin verla".

Se aplica también a: agentes que dejan de reportar, helpers que dejan de escribir
al spool, y collectors que dejan de producir muestras.

#### 4. Deadman externo del propio hub

El hub hace ping periódico a un servicio externo (ntfy o una instancia de
healthchecks alojada **en el VPS**). Si iCloudServer se cuelga, el aviso llega
desde fuera. Sin esto, un cuelgue del hub es indistinguible de "todo va bien".

Son unas pocas decenas de líneas y resuelve el punto ciego más grave de la
arquitectura (ADR-0002).

### Notificadores

Interfaz común, con estas implementaciones en el MVP:

| Notificador | Nota |
|---|---|
| **ntfy** | por defecto; self-hosted, con app iOS y Android nativas |
| Webhook genérico | integración con cualquier cosa, incluido Task Queue AI |
| Telegram | práctico y sin infraestructura |
| Correo (SMTP) | supervivencia |
| Log de sistema | siempre activo, no configurable |

Enrutado por severidad y por etiqueta: `critical` a ntfy y Telegram, `warn` solo
a ntfy, `info` solo al histórico.

### Dónde se evalúan las reglas

**En el hub, no en el agente.** El agente no conoce las reglas. Motivos: las
reglas cambian sin redesplegar agentes, pueden cruzar hosts ("el backup salió de
A pero no llegó a B"), y mantiene al agente simple y ligero.

Excepción: los eventos de severidad `critical` se envían de inmediato saltándose
el lote (ADR-0008), para que la latencia de notificación no dependa del ciclo de
10 s.

### Integración con Task Queue AI

Vía el notificador webhook, con un payload que incluye el evento completo, el
contexto de la línea temporal (±15 minutos de métricas relevantes) y un enlace
profundo a la vista de diagnóstico. Se define en fase 4, pero el payload se
diseña ya para que no haga falta cambiarlo después.

## Alternativas consideradas

- **Alertmanager de Prometheus.** Descartado como dependencia: es otro servicio,
  y su modelo no cubre las reglas sobre eventos ni los deadman de jobs, que son
  la mitad del valor aquí. Sí se toma prestado su modelo conceptual (`for`,
  agrupación, silencios, inhibición), que está bien diseñado.
- **Alertas evaluadas en el agente.** Descartado: obliga a distribuir la
  configuración, impide reglas entre hosts y engorda el agente.
- **Umbral simple sin histéresis ni `for`.** Descartado: es la causa directa de
  la fatiga de alertas.
- **Machine learning para detección de anomalías desde el MVP.** Descartado por
  ahora. La detección estadística simple (media móvil, desviación robusta con
  MAD, comparación con la misma hora de días anteriores) cubre la mayoría de los
  casos, es explicable y no requiere entrenamiento. Un modelo que dice "anomalía"
  sin decir por qué es inútil para diagnosticar. Fase 3.
- **APNs directo como notificador.** Descartado. Ver ADR-0014.

## Consecuencias

### Positivas
- Los deadman cubren el fallo silencioso, que es el más peligroso y el que hoy
  no se detecta.
- El deadman externo elimina el punto ciego de un hub caído.
- El historial de transiciones convierte las alertas en datos de diagnóstico,
  no solo en avisos.

### Negativas
- La máquina de estados con histéresis, silencios y agrupación es
  significativamente más código que un `if valor > umbral`. Es la complejidad
  que hay que aceptar para que el sistema siga siendo útil pasados seis meses.
- Los deadman requieren declarar la periodicidad esperada de cada job. Es
  configuración manual y se olvidará. Mitigación: el sistema **propone**
  automáticamente reglas deadman al observar un job que se ha ejecutado con
  cadencia regular tres veces, y el usuario solo confirma.
- Depender de ntfy como canal por defecto añade un servicio, aunque sea ligero y
  self-hosted.

## Notas de implementación

- Las reglas por defecto van en `rules/` dentro del paquete; las del usuario en
  `/etc/bitacora/rules/`. Las del usuario nunca se sobrescriben en una
  actualización.
- Toda regla se puede **probar contra datos históricos** antes de activarla:
  `bita rules test <fichero> --since 30d` responde cuántas veces habría
  disparado. Sin esto no hay forma razonable de calibrar umbrales.
- Límite de tasa de notificaciones por canal, con techo duro. Un bucle de
  alertas no debe poder enviar mil mensajes.
- Toda alerta notificada incluye enlace profundo a la línea temporal centrada en
  su instante. La notificación sin contexto obliga a abrir un SSH, que es
  justamente lo que se quiere evitar.
