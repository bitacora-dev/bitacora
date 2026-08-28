# ADR-0007: Interfaz `Collector` y ciclo de vida

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

Se prevén más de veinte collectors, escritos en buena parte por agentes de IA en
ramas separadas y por contribuidores externos. Sin un contrato estricto, cada
uno resolverá a su manera los errores, los intervalos, el estado entre
ejecuciones y las capacidades ausentes. El resultado sería un agente que a veces
se cuelga en una llamada a `smartctl`, a veces filtra goroutines y a veces
duplica trabajo.

## Decisión

### Contrato

```go
type Collector interface {
    // Identidad estable. Aparece en métricas, eventos y configuración.
    Name() string

    // Capacidades requeridas (ADR-0004). Si falta alguna, el runtime no
    // registra el collector y emite un evento agent.collector_disabled.
    Requires() []Capability

    // Preparación: abrir ficheros, resolver rutas, validar config.
    // Un error aquí desactiva el collector; no aborta el agente.
    Init(ctx context.Context, cfg Config, host *HostInfo) error

    // Una recolección. DEBE respetar la cancelación del contexto.
    // NO debe bloquear más allá del deadline del contexto.
    Collect(ctx context.Context, sink Sink) error

    // Liberación de recursos.
    Close() error
}

type Sink interface {
    Gauge(name string, value float64, labels Labels)
    Counter(name string, value float64, labels Labels)
    Event(e Event)
    LogLines(source string, lines []LogLine)
}
```

### Reglas de ejecución, impuestas por el runtime

1. **Cada collector es una goroutine con su propio ticker.** Los intervalos son
   independientes y configurables por collector.
2. **Timeout duro.** El runtime cancela el contexto a `min(intervalo × 0.8,
   timeout_configurado)`. Un `Collect` que ignore la cancelación se contabiliza,
   se reporta y, a la tercera, desactiva el collector.
3. **Un pánico en un collector no tumba el agente.** El runtime lo recupera, lo
   registra como evento de nivel `error` y aplica backoff.
4. **Backoff exponencial ante errores repetidos**: ×2 hasta un máximo de 10
   intervalos. Al recuperarse vuelve al intervalo normal y emite evento de
   resolución.
5. **Los collectors no acceden al almacenamiento.** Solo escriben al `Sink`. No
   conocen el hub, ni la red, ni la base de datos.
6. **Los collectors no escriben a disco** salvo su cursor, y solo a través del
   `StateStore` que provee el runtime.
7. **Sin estado global.** Nada de variables de paquete mutables. Todo el estado
   vive en el struct del collector.
8. **Reloj inyectado**, nunca `time.Now()` directamente, para que los tests sean
   deterministas.

### Intervalos por defecto

| Clase | Ejemplos | Intervalo |
|---|---|---|
| Alta frecuencia | CPU, RAM, load, red | 10 s |
| Media | procesos, cgroups de contenedor, systemd | 30 s |
| Baja | filesystems, actualizaciones pendientes | 5 min |
| De spool | SMART, mdadm, SnapRAID | según su timer |
| De flujo | journald, eventos de Docker | continuo, `LogLines` en lotes |
| Caja negra | ADR-0011 | 1 s, camino separado |

### Métricas obligatorias por collector

El runtime las emite automáticamente. El collector no las escribe:

- `bitacora_collector_duration_seconds{collector}`
- `bitacora_collector_errors_total{collector}`
- `bitacora_collector_last_success_timestamp{collector}`
- `bitacora_collector_samples_total{collector}`

### Estructura en disco

```
internal/collector/
├─ collector.go           # interfaz, tipos, Sink
├─ runtime.go             # scheduler, timeouts, backoff, recover
├─ registry.go            # registro y resolución por capacidades
├─ cpu/
│  ├─ cpu.go
│  ├─ cpu_test.go
│  └─ testdata/proc_stat_i9_13900k
├─ memory/
├─ journald/
├─ docker/
└─ ...
```

Cada collector: un paquete, tests con `testdata/` de fixtures **reales**
(volcados de `/proc` y de comandos de las máquinas del parque), y un
`README.md` que documente qué métricas y eventos emite y qué capacidades exige.

### Requisitos para aceptar un collector nuevo

Checklist obligatoria en la PR:

- [ ] Declara `Requires()` correctamente.
- [ ] Tests con fixtures reales, sin acceso a `/proc` del runner de CI.
- [ ] Documenta cardinalidad máxima y la respeta (ADR-0006).
- [ ] Respeta la cancelación del contexto (test explícito).
- [ ] No hace `exec` de comandos externos sin pasar por el mecanismo de helpers
      (ADR-0005).
- [ ] `README.md` del paquete con el catálogo de métricas y eventos.
- [ ] Fixture de la plataforma más rara donde deba funcionar.

## Alternativas consideradas

- **Collectors como plugins externos** (`.so` con `plugin`, o subprocesos con
  protocolo). Descartado para el MVP: el `plugin` de Go tiene restricciones
  severas de versión y sistema operativo, y los subprocesos multiplican por N
  el consumo, que es justo lo que se quiere evitar. Reevaluable en fase 4 con
  WASM, que sí encajaría.
- **Un scheduler central con un pool de workers.** Descartado: introduce
  encolamiento y bloqueo en cabeza; un collector lento retrasaría a todos. Una
  goroutine por collector aísla mejor y su coste en Go es despreciable.
- **Que los collectors escriban directamente al almacenamiento.** Descartado:
  rompe la separación agente/hub (ADR-0002) e imposibilita testear un collector
  sin base de datos.
- **Interfaz con `Describe()` a lo Prometheus.** Descartado por innecesario: el
  catálogo de métricas se documenta en el `README.md` del paquete y se valida
  con un test de snapshot, sin coste en runtime.

## Consecuencias

### Positivas
- Un agente de IA puede implementar un collector nuevo leyendo un solo fichero
  de interfaz y un ejemplo, sin entender el resto del sistema.
- Un collector defectuoso degrada su propia función, no el agente.
- Los tests con fixtures reales permiten desarrollar el soporte de UnRaid sin
  tener un UnRaid delante.

### Negativas
- El contrato es rígido: un collector con necesidades atípicas (la caja negra,
  por ejemplo) requiere un camino aparte, y de hecho lo tiene (ADR-0011).
- Una goroutine por collector con más de 20 collectors implica vigilar fugas.
  Test obligatorio de conteo de goroutines tras `Close()`.
- Mantener fixtures reales exige recolectarlos de máquinas concretas y
  anonimizarlos.

## Notas de implementación

- Escribir **primero** `internal/collector/example/` como referencia canónica,
  con todos los patrones correctos. Es el fichero que leerán los agentes de IA
  antes de implementar cualquier otro.
- `bita collectors list` muestra registrados, activos, desactivados y el
  motivo de la desactivación. Complementa a `bita doctor` (ADR-0005).
- Prohibido `time.Sleep` dentro de `Collect`. Si hace falta esperar, es
  `select` sobre el contexto.
