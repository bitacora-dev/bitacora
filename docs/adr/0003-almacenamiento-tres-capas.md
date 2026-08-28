# ADR-0003: Almacenamiento en tres capas

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El sistema genera tres tipos de dato con perfiles radicalmente distintos.
Estimación para cuatro hosts, con muestreo a 10 s:

| Tipo | Volumen crudo / año | Patrón de escritura | Patrón de lectura |
|---|---|---|---|
| Métricas numéricas | 8-15 GB (~1500 series) | append masivo, regular | rangos temporales, agregación |
| Eventos / jobs / alertas | 200-800 MB | append moderado, irregular | filtros relacionales, joins |
| Logs (journald + Docker) | **80-240 GB** | append brutal | búsqueda de texto acotada por tiempo |

Los logs son uno o dos órdenes de magnitud mayores que todo lo demás junto.

Meter texto de log en filas de una base de datos relacional es un error
conocido, con SQLite y con PostgreSQL por igual: un índice de texto completo
sobre cientos de millones de filas ocupa más que los propios datos, el
mantenimiento del índice domina el coste de escritura, y el borrado por
retención fragmenta el almacén. Es el motivo por el que existen Loki y
herramientas equivalentes.

## Decisión

Tres motores, todos embebidos, sin ninguna dependencia externa obligatoria.

### Capa 1 — Métricas: `prometheus/tsdb`

El motor de Prometheus usado como biblioteca, no como servicio. Compresión
Gorilla (~1,3 bytes por muestra), compactación y retención automáticas.

Downsampling propio en tres resoluciones:

| Resolución | Retención por defecto |
|---|---|
| 10 s (cruda) | 7 días |
| 1 min | 90 días |
| 5 min | 2 años |

Con esto, 1500 series ocupan del orden de 3-5 GB para dos años de histórico.

### Capa 2 — Logs: almacén de bloques comprimidos

Implementación propia, deliberadamente simple:

- Las líneas se acumulan en memoria por `(host_id, fuente)` hasta completar
  ~5 MB o hasta que pasan 5 minutos.
- El bloque se comprime con **zstd nivel 3** y se escribe a
  `/var/lib/bitacora/logs/<host_id>/<AAAA>/<MM>/<DD>/<ulid>.zst`.
- En la base relacional se indexa **solo el metadato**: `block_id`, `host_id`,
  `source`, `ts_min`, `ts_max`, `unit`/`container`, `n_lines`, `levels_bitmap`,
  `path`, `size_raw`, `size_compressed`.

Buscar = filtrar bloques candidatos por índice + descomprimir solo esos +
`grep`/regex en memoria. Zstd descomprime a >1 GB/s por core, de modo que
rastrear una hora concreta de una unidad concreta es del orden de decenas de
milisegundos.

Ratio de compresión observado en logs de texto: 8-12×. Los 80-240 GB crudos
quedan en 8-25 GB.

La retención es borrar directorios. No hay `VACUUM`, ni bloat, ni fragmentación.

### Capa 3 — Datos relacionales: SQLite por defecto, PostgreSQL opcional

Eventos, jobs, alertas y su historial de estados, hosts, manifiestos de
capacidades, índice de bloques de log, configuración, usuarios y tokens.

- **Backend por defecto: SQLite** en modo WAL, con `synchronous=NORMAL`,
  `busy_timeout=5000` y FTS5 disponible para el índice de eventos.
- **Particionado por fichero**: una base por mes
  (`/var/lib/bitacora/db/events-2026-08.db`), con `ATTACH` para consultas que
  crucen meses. La retención vuelve a ser borrar un fichero. Esto elimina el
  único punto débil real de SQLite en este caso de uso, que es el borrado masivo
  por retención.
- **Backend opcional: PostgreSQL**, activable en configuración, con la misma
  interfaz, las mismas migraciones y cobertura en CI equivalente.

Se define una interfaz `storage.Relational` desde el primer commit y **ambas
implementaciones se mantienen verdes en CI desde el principio**. No es un plan
para después: es una restricción de diseño desde el día uno.

## Alternativas consideradas

- **Todo en PostgreSQL / MariaDB / MySQL, sin tsdb ni bloques.** Descartado. No
  resuelve el problema real, que son los logs, y lo empeora: una tabla de
  cientos de millones de filas con GIN de texto completo sufre de autovacuum
  permanente y consultas de búsqueda lentas. Cambiar SQLite por MariaDB movería
  el cuello de botella de sitio sin eliminarlo.
- **Reutilizar la instancia de PostgreSQL existente del servidor.** Descartado
  con firmeza. Acopla la herramienta de diagnóstico al servicio que debe
  diagnosticar: si ese PostgreSQL cae o se degrada, el sistema se queda ciego
  exactamente cuando hace falta. Si se usa el backend PostgreSQL, debe ser una
  **instancia dedicada** (contenedor, puerto y volumen propios).
- **TimescaleDB para todo.** Descartado para el MVP: obliga a PostgreSQL como
  dependencia dura, lo que mata la instalación de un tercero en cinco minutos.
  Queda como opción natural si algún día el backend PostgreSQL se vuelve
  mayoritario.
- **Loki / VictoriaLogs para los logs.** Descartados como dependencia
  obligatoria: son otro servicio que instalar, configurar y mantener. La capa de
  bloques comprimidos cubre el caso de uso con unos pocos cientos de líneas de
  código. Se deja la puerta abierta a un backend `LogStore` alternativo.
- **Prometheus externo en lugar de tsdb embebido.** Descartado: obliga a
  desplegar y configurar Prometheus, y el modelo pull no encaja (ver ADR-0002).
- **MariaDB / MySQL como backend relacional opcional en lugar de PostgreSQL.**
  Descartado. PostgreSQL aporta cuatro cosas que MariaDB no tiene y que aquí
  importan: índices **BRIN** sobre timestamps (para datos ordenados por tiempo
  ocupan una fracción ínfima de un B-tree), **particionado nativo por rango** con
  `DETACH PARTITION` para retención instantánea, **JSONB con GIN** para el campo
  de atributos flexible de los eventos, y la vía a TimescaleDB.

## Consecuencias

### Positivas
- Instalación por defecto sin ninguna dependencia externa: un binario y un
  directorio de datos.
- La retención, tanto de métricas como de logs, es una operación de sistema de
  ficheros. Sin mantenimiento periódico.
- El camino a PostgreSQL existe y está probado desde el principio, no es una
  promesa.
- Presupuesto de disco total estimado para 4 hosts y 2 años: **15-35 GB**.

### Negativas
- **Tres motores que mantener** en lugar de uno. Es la consecuencia más cara de
  este ADR y hay que asumirla conscientemente.
- El almacén de bloques es **código propio**: los bugs son nuestros. Se mitiga
  con un formato deliberadamente tonto (bloques inmutables, un índice
  reconstruible escaneando el directorio) y con un comando
  `bita logs verify`.
- Con SQLite, las consultas que cruzan muchos meses requieren `ATTACH` múltiple,
  con un límite práctico (por defecto 10 bases adjuntas, configurable). Consultas
  de más de 10 meses de rango necesitan iterar por lotes.
- Mantener dos backends relacionales verdes en CI cuesta tiempo de build y
  disciplina en las migraciones (nada de SQL específico de motor fuera de la
  capa de adaptación).

## Notas de implementación

- Umbral de migración a PostgreSQL, para documentar en la guía de operación: en
  torno a **10 agentes escribiendo simultáneamente**, o antes si el volumen de
  eventos supera ~50 escrituras/segundo sostenidas.
- `bita migrate --to postgres` debe existir y estar probado antes de
  anunciar el backend como soportado.
- Presupuesto de disco con **tope duro configurable**. Al alcanzarlo, el sistema
  degrada por orden: primero borra logs antiguos, luego métricas de resolución
  cruda, y **nunca** eventos ni alertas. Debe emitir un evento de nivel `warn`
  antes de borrar nada.
- Todas las escrituras a SQLite pasan por una única goroutine con cola. Los
  lectores son concurrentes. Esto elimina la contención de escritura, que es el
  fallo clásico de SQLite bajo concurrencia.
