# ADR-0011: Caja negra y diagnóstico de cuelgues

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El incidente que motiva este proyecto: iCloudServer sufrió un cuelgue duro. El
journal del arranque anterior termina abruptamente sin apagado limpio. Al
reiniciar, `md/raid1:md0: not clean -- starting background reconstruction`. No
había OOM, ni carga alta, ni errores NVMe, ni errores EXT4, ni MCE, ni panic
registrado. Instantes antes: CPU al 99,7 % ociosa, load average 0,2, 61 GB de RAM
disponible.

Durante días previos se observaron segfaults de Node y Python coincidiendo
repetidamente con la CPU lógica 8. Como prueba diagnóstica se dejaron offline las
CPU 8 y 9, que pertenecen al mismo P-core.

**Ninguna herramienta de observabilidad convencional habría capturado nada útil
de este incidente**, por dos razones estructurales:

1. Un cuelgue duro no escribe en journald por definición: el kernel muere antes
   de hacer flush de sus buffers. Lo último persistido es de hace segundos o
   minutos.
2. Con muestreo cada 10 o 30 segundos, los últimos instantes —que son los
   únicos que importan— simplemente no existen.

## Decisión

Cuatro mecanismos complementarios. Esta es la funcionalidad diferencial del
proyecto; nada de lo que hay en el ecosistema open source lo resuelve bien.

### 1. Caja negra (grabadora de alta frecuencia)

Buffer circular en memoria, muestreado **a 1 Hz**, volcado a disco cada 5 s
mediante un fichero mapeado en memoria (`mmap`) con escritura atómica.

Contenido, deliberadamente acotado a lo que se puede leer barato:

- Utilización por CPU lógica (las 32 del i9-13900K, individualmente)
- Frecuencia por core y estado de throttling
- Temperaturas de todos los sensores hwmon
- Memoria: total, disponible, cacheada, swap, `dirty`, `writeback`
- Load average, procesos ejecutables, procesos en `D` (bloqueados en E/S)
- Interrupciones por CPU (deltas de `/proc/interrupts`)
- Presión PSI (`/proc/pressure/{cpu,memory,io}`) — el indicador más temprano de
  degradación disponible en Linux
- Profundidad de cola y latencia por dispositivo de bloque
- Contadores de EDAC (errores ECC)

**Retención: 15 minutos** de historia a 1 s. Al arrancar tras un reinicio no
limpio, el agente detecta el fichero, lo ingiere como métricas de alta
resolución y lo marca como *ventana pre-fallo*.

Restricciones de implementación:

- Buffer **preasignado**, sin asignaciones en el camino caliente. Sin presión de
  GC, para que la propia grabadora no sea la que se degrade cuando el sistema
  está sufriendo.
- Coste objetivo: **< 0,5 % de un core y < 10 MB**.
- Camino de código **separado del runtime de collectors** (ADR-0007): no pasa por
  el `Sink` ni por el buffer de salida. Es la única excepción admitida a esa
  interfaz, y es deliberada: debe sobrevivir a un agente degradado.

### 2. pstore / ramoops

Se reserva una región de RAM que sobrevive al reinicio. El kernel vuelca ahí el
oops o el panic; el agente lo lee de `/sys/fs/pstore` al arrancar, lo convierte
en evento `kernel.crash_dump` y limpia la región.

Es el mecanismo que **sí** habría capturado la causa del incidente si hubo un
oops silencioso.

Requiere configuración en el arranque (parámetro `ramoops.mem_address` o Device
Tree). El agente lo detecta y, si no está, lo reporta como capacidad degradada
con enlace a la guía de configuración (ADR-0004).

### 3. netconsole

El kernel envía sus mensajes por UDP a otra máquina en tiempo real. Configuración
para iCloudServer: destino **UnRaid**. Si el i9 se congela, los últimos mensajes
del kernel ya están a salvo en otra máquina.

El hub incluye un receptor de netconsole que ingiere esas líneas como
`source: kernel_remote`, etiquetadas con el `host_id` de origen.

Es la única forma de obtener mensajes del kernel de una máquina que ya no puede
escribir en su propio disco.

### 4. Correlación de segfaults con topología de CPU

Un collector específico que:

- Extrae de journald cada segfault con `comm`, `pid`, `ip`, `sp`, `error`.
- Determina la **CPU lógica** donde ocurrió, vía `/proc/<pid>/stat` (campo 39,
  `processor`) si el proceso sigue vivo, o por contexto del mensaje.
- Cruza con la topología leída de `/sys/devices/system/cpu/`: mapea CPU lógica →
  core físico → tipo (P-core / E-core) → hermano SMT.
- Mantiene un contador por core y aplica una **prueba binomial** contra la
  distribución esperada si los fallos fueran uniformes.
- Emite `hw.cpu_fault_cluster` cuando la desviación es significativa
  (p < 0,01 con al menos 5 muestras).

Esto convierte "hay segfaults sueltos" en "**34 segfaults en 6 días, 31 de ellos
en el core físico 4 (CPU 8/9); la probabilidad de que sea azar es del 0,0001 %**".
Es exactamente el razonamiento que se hizo a mano, automatizado.

El collector también registra qué CPU están offline y desde cuándo, para que la
prueba diagnóstica en curso quede documentada en la línea temporal.

### 5. Vista de post-mortem

Al detectar un arranque tras un apagado no limpio, el hub genera automáticamente
un **informe de incidente** con:

- Ventana de la caja negra a 1 s de los 15 minutos previos
- Últimas líneas de journald antes del corte, y de netconsole si existen
- Volcado de pstore si existe
- Estado del RAID al arrancar y progreso del resync
- Eventos de las 24 h previas, agrupados por fingerprint
- Comparación con el comportamiento típico de la misma franja horaria
- Contadores de EDAC y MCE antes y después

Accesible como una URL permanente y compartible.

## Alternativas consideradas

- **Kdump.** Da mucha más información (volcado completo de memoria), y se
  descarta como requisito: necesita un kernel de rescate reservado, decenas de GB
  de espacio y configuración delicada. Se documenta como recomendación opcional
  para quien quiera ir más lejos, pero no se depende de él.
- **Aumentar la frecuencia de muestreo general a 1 s.** Descartado: multiplicaría
  por 10 el volumen de todas las series permanentemente, para obtener un dato que
  solo importa en la ventana previa a un fallo. La caja negra da la resolución
  solo donde hace falta y no persiste nada mientras no ocurra nada.
- **Un watchdog hardware que reinicie y registre.** Complementario, no
  alternativo. Se documenta cómo habilitar el watchdog de Intel TCO y se
  registran sus eventos, pero no resuelve el diagnóstico por sí solo.
- **Delegar en `rasdaemon` para todo el análisis de hardware.** No es
  alternativa sino insumo: se recomienda instalarlo y se ingiere su base de
  datos, pero no da la correlación con la topología de CPU ni el análisis
  estadístico.

## Consecuencias

### Positivas
- El sistema puede diagnosticar la clase de incidente que hoy es imposible de
  investigar. Es la razón de existir del proyecto.
- El análisis de agrupamiento de fallos por core es, hasta donde alcanza el
  estado del arte open source, funcionalidad inédita, y es el tipo de cosa que
  atrae contribuidores.
- El informe de post-mortem convierte un cuelgue en un documento revisable en
  lugar de en una sesión de arqueología por SSH.

### Negativas
- La caja negra escribe a disco cada 5 s de forma permanente. En un SSD es
  irrelevante (unos pocos MB al día); en una tarjeta SD sería dañino, y hay que
  documentarlo y permitir desactivarla.
- ramoops y netconsole requieren **configuración manual del arranque**, con
  reinicio. No se puede automatizar de forma segura y hay que guiar al usuario.
- La correlación segfault → CPU es **best-effort**: si el proceso ya murió, el
  dato puede no estar disponible. La tasa de éxito debe mostrarse, no ocultarse.
- Es la parte más compleja del proyecto y la más difícil de testear, porque
  requiere provocar fallos reales.

## Notas de implementación

- Test de la caja negra: inyectar `SIGKILL -9` al agente y verificar que el
  fichero mapeado contiene datos coherentes hasta el último volcado.
- El formato del fichero de caja negra debe ser **legible sin el agente**:
  `bita blackbox dump <fichero>` debe funcionar sobre un fichero copiado
  desde otra máquina, incluso de otra versión.
- Guía de configuración obligatoria en `docs/setup/`: `ramoops.md`,
  `netconsole.md`, `watchdog.md`, `rasdaemon.md`.
- **Verificar hoy, antes de nada**: que `Storage=persistent` esté activo en
  `journald.conf` de iCloudServer. Sin eso se pierden los boots anteriores y el
  post-mortem se queda sin su fuente principal.
