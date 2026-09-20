# ADR-0024: Evidencia reproducible del presupuesto de recursos del agente

- **Estado:** Aceptado
- **Fecha:** 2026-09-19

## Contexto

ADR-0001 fija el límite del agente en **≤ 60 MiB RSS** y **≤ 2 % de un
core** en régimen permanente. El test existente iniciaba el binario con rutas
de producción, ignoraba que pudiera terminar antes de medir y calculaba CPU a
partir del tiempo acumulado del proceso. Por tanto podía hacer `Skip` sin
aportar evidencia y no medía una tasa de CPU.

La medición manual disponible para el conjunto actual estabilizó en **72.6
MiB RSS**, con un pico de **84.1 MiB RSS**. Ambos valores superan el límite de
ADR-0001; son evidencia del estado observado, no una atribución inventada a
un collector concreto.

## Decisión

El agente monitoriza su PID en régimen permanente. Conserva una muestra base y
calcula CPU como `(cpu_final - cpu_inicial) / (tiempo_final - tiempo_inicial)`.
Cuando RSS o esa tasa supera el límite de ADR-0001, emite una sola vez el
evento `agent.resource_budget_exceeded`, con los valores observados.

La prueba de presupuesto usa rutas temporales para estado del agente, falla si
el proceso termina antes de cualquiera de las dos muestras y comprueba el
catálogo completo de collectors ensamblados. El catálogo se expone con nombres
ordenados para que el resultado no dependa del orden de registro ni de las
capacidades de la máquina que ejecuta la prueba.

El RSS de `/proc/<pid>/status` es la medida autoritativa del límite. Un posible
desglose de heap de Go sólo puede etiquetarse como memoria gestionada,
compartida o residual no atribuible: `runtime.MemStats` es global al proceso y
las etiquetas de `runtime/pprof` no se aplican a perfiles heap. No se asigna
RSS a collectors individuales sin una medición aislada que lo demuestre.

## Alternativas consideradas

- **Dividir RSS entre collectors activos.** Rechazado: las asignaciones del
  runtime, stacks, cachés y memoria nativa son compartidas; el reparto sería
  ficticio.
- **Etiquetas pprof por collector para heap.** Rechazado: Go sólo usa esas
  etiquetas en perfiles de CPU y goroutine, no en heap.
- **Mantener una única muestra de CPU.** Rechazado: `/proc/<pid>/stat` informa
  CPU acumulada desde el arranque y no permite derivar una fracción temporal.

## Consecuencias

### Positivas

- La evidencia de CPU corresponde a una ventana temporal real.
- Un agente que no llega a estado estacionario ya no convierte el fallo en un
  `Skip` silencioso.
- El evento deja una señal durable y acotada para investigar un exceso.

### Negativas

- El monitor añade una goroutine y una lectura de `/proc` cada diez segundos.
- El test de integración sigue dependiendo de Linux y de la carga real del
  runner para los valores de RSS.
- El evento informa el exceso total, no una causa por collector.

## Notas de implementación

- La alerta no se rearma durante el mismo proceso: se emite una vez por vida
  para evitar ruido mientras persista el exceso.
- Si se necesita investigar por origen, se ejecutan perfiles de heap en
  procesos aislados y fixtures idénticos; sus resultados no cambian el límite
  RSS ni se presentan como atribución exacta.
