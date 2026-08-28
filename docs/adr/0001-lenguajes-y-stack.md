# ADR-0001: Lenguajes y stack tecnológico

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El sistema tiene tres piezas con requisitos distintos:

1. **Agente**: corre permanentemente en cada máquina observada. Debe consumir muy
   poco (es inaceptable que la observabilidad sea una carga notable), instalarse
   sin dependencias y funcionar en Ubuntu, AlmaLinux, UnRaid y un VPS.
2. **Hub**: ingesta, almacenamiento, motor de alertas y API.
3. **Frontend**: interfaz gráfica moderna, con series temporales densas.

Restricciones del proyecto: open source, instalable por terceros, sin
dependencias SaaS, bajo consumo.

## Decisión

- **Agente y hub en Go** (versión mínima 1.22).
- **Frontend en TypeScript + React + Vite + TailwindCSS**, con **uPlot** para
  gráficas de series temporales.
- El frontend se compila y se **embebe en el binario del hub** con `go:embed`.
- Distribución: un binario estático por componente, más paquetes `.deb` y `.rpm`
  generados con `nfpm`.

Bibliotecas base aceptadas para el agente:

| Necesidad | Biblioteca |
|---|---|
| procfs / sysfs | `github.com/prometheus/procfs` |
| métricas de sistema portables | `github.com/shirou/gopsutil/v4` |
| journald | `github.com/coreos/go-systemd/v22` (sdjournal) o `journalctl -o json` |
| D-Bus systemd | `github.com/godbus/dbus/v5` |
| SQLite sin CGO | `modernc.org/sqlite` |
| TSDB | `github.com/prometheus/prometheus/tsdb` |
| compresión de logs | `github.com/klauspost/compress/zstd` |

`modernc.org/sqlite` es una decisión deliberada: es SQLite traducido a Go puro,
lo que permite compilación cruzada sin toolchain de C. Es más lento que
`mattn/go-sqlite3` (del orden de un 30-50 % en escritura), pero el volumen de
escritura relacional es bajo (ver ADR-0003) y la simplicidad de build compensa.

## Alternativas consideradas

- **Node.js / TypeScript en el agente.** Descartado. Residente base de 80-150 MB
  frente a 25-50 MB de Go; requiere runtime instalado o empaquetado con `pkg`;
  el acceso a procfs y a D-Bus pasa por bindings nativos que rompen la
  compilación cruzada. Un runtime completo para leer ficheros de texto de
  `/proc` es desproporcionado.
- **Rust en el agente.** Técnicamente superior: sin GC, footprint menor,
  `heim`/`sysinfo` son buenas. Descartado por dos motivos: (a) la ventaja real
  frente a Go en este perfil de carga —I/O de ficheros pequeños con parseo— es
  de pocos MB y algún punto porcentual de CPU, irrelevante frente al presupuesto
  fijado; (b) reduce mucho el conjunto de contribuidores potenciales en un
  proyecto que quiere adopción externa. Reevaluable si el agente llega a ser
  un cuello de botella medido, no antes.
- **Python.** Descartado sin discusión para el agente: dependencia de intérprete,
  arranque lento, footprint alto.
- **Un único binario monolítico.** Ver ADR-0002.
- **Recharts / Chart.js para las gráficas.** Descartados: ambos se degradan de
  forma visible por encima de unos pocos miles de puntos. uPlot dibuja cientos de
  miles con un coste de bundle de ~50 KB.
- **Frontend servido aparte (nginx).** Descartado: obliga a una segunda pieza en
  el despliegue. `go:embed` permite que desplegar sea copiar un fichero.

## Consecuencias

### Positivas
- Instalación real de un tercero: descargar un binario y un `.service`.
- Compilación cruzada trivial para amd64 y arm64 (relevante para UnRaid y para
  futuros Raspberry / NAS).
- El ecosistema de observabilidad en Go (procfs, tsdb) está muy probado en
  producción y ahorra meses de parseo propio.

### Negativas
- Go tiene GC: hay pausas, aunque en el orden de microsegundos e irrelevantes
  aquí. Importa en un solo sitio, la caja negra (ADR-0011), donde el buffer debe
  preasignarse para no generar basura en el camino caliente.
- Dos lenguajes en el repo implica dos toolchains en CI y dos culturas de
  linting.
- `modernc.org/sqlite` es más lento y menos probado que el binding CGO. Si el
  perfilado lo señala, existe la vía de escape de compilar con `mattn` bajo una
  build tag.

## Notas de implementación

- Presupuesto de recursos del agente, verificable en CI con un test de carga:
  **≤ 60 MB RSS y ≤ 2 % de un core** en régimen permanente con el conjunto
  completo de collectors a 10 s.
- El agente expone sus propias métricas de consumo como cualquier otro collector
  (ver ADR-0007). La auto-observabilidad no es opcional.
- Prohibido en el agente: cualquier dependencia que requiera CGO por defecto.
