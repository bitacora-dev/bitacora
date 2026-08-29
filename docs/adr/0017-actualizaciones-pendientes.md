# ADR-0017: Paquetes, plugins y contenedores desactualizados

- **Estado:** Aceptado
- **Fecha:** 2026-08-29

## Contexto

ADR-0004 ya nombra la interfaz `PackageUpdates → apt | dnf | unraid_plugins`
como parte del diseño desde el principio, pero nunca se implementó. La
pregunta que la motiva es concreta: qué paquetes, plugins de UnRaid y
contenedores Docker tiene instalados cada host, cuáles tienen una versión
más nueva disponible, y — cuando sea posible saberlo — cuánto de
desactualizados están.

Las cuatro fuentes (apt, dnf, plugins de UnRaid, imágenes Docker) no tienen
la misma dificultad. Conviene decirlo aquí en vez de descubrirlo a medio
implementar.

## Decisión

### apt (Debian/Ubuntu): lectura pura, sin `exec`

`/var/lib/dpkg/status` da la versión instalada de cada paquete. La caché
local de metadatos de apt (`/var/lib/apt/lists/*_Packages`, la misma que
`apt update` ya mantiene por su cuenta) da la versión candidata por
repositorio configurado. Comparando ambas con la misma lógica de
comparación de versiones que usa `dpkg` (no una comparación de cadenas
ingenua — los esquemas de versión de Debian tienen reglas propias) se
obtiene la lista de paquetes desactualizados sin ejecutar nada.

La antigüedad de esa caché importa: si `apt update` lleva días sin
correr, el dato está obsoleto. Se reporta la fecha de la caché junto al
resultado — capacidad degradada (ADR-0004), no un dato presentado como
fresco cuando no lo es.

### dnf (AlmaLinux): más difícil sin helper

La base de datos RPM instalada (`/var/lib/rpm`) es igual de directa que
`dpkg`. El problema es la caché de metadatos de repositorio de DNF: no es
un fichero de texto simple como el de apt, es XML comprimido o SQLite
dentro de `/var/cache/dnf/*/repodata/` — parsearlo sin ninguna dependencia
adicional es sustancialmente más trabajo que el caso de apt.

Se decide: helper privilegiado nuevo (patrón `bitacora-smart`, ADR-0005)
que ejecuta `dnf check-update` — consulta cerrada, sin parámetros externos,
exactamente el tipo de comando que ADR-0012 permite a un helper. No lee
`exec` desde el agente en ningún caso, igual que el resto del proyecto.

### Plugins de UnRaid: lectura local + una petición de red por plugin

Cada plugin instalado en UnRaid deja un fichero `.plg` local en
`/boot/config/plugins/` con su versión instalada. Su definición también
vive en una URL remota (el propio repositorio del plugin) que declara la
versión actual. Sin esa segunda parte no hay forma de saber si hay una
versión más nueva — a diferencia de apt/dnf, UnRaid no mantiene una caché
local de "qué hay disponible", cada plugin apunta a su propia fuente.

### Imágenes Docker: la más cara, mismo patrón

Igual que los plugins de UnRaid: la imagen local tiene su propio digest
(`sha256:...`, ya visible en el store de Docker); saber si hay una más
nueva exige consultar el registro (Docker Hub, GHCR, u otro) por su
digest actual. A diferencia de un plugin de UnRaid, esto puede toparse con
límites de tasa de peticiones anónimas del registro (conocido en Docker
Hub) y con registros privados que necesitan credenciales — se resuelve por
imagen, con backoff, y se acepta que algunas imágenes queden en "no se
pudo comprobar" en vez de forzar autenticación.

### "Cuánto de desactualizado"

Se expresa con la semántica de versión propia de cada gestor (comparación
de versiones de `dpkg`/`rpm`, no una resta de números), y — cuando el
propio origen lo permite saber, que no es siempre — la fecha de
publicación de la versión candidata frente a la instalada. Cuando no se
puede saber la fecha, se muestra solo el salto de versión, sin inventar
una antigüedad.

## Alternativas consideradas

- **`exec` de `apt list --upgradable` en vez de leer la caché
  directamente.** Descartado para apt porque la lectura directa ya es
  viable y evita abrir otra excepción a ADR-0012 cuando no hace falta. Sí
  se acepta para dnf, donde la lectura directa deja de ser razonable.
- **Forzar `apt update`/`dnf makecache` antes de cada consulta.**
  Descartado: es una escritura activa contra el sistema del operador
  (tráfico, carga en los espejos) disparada por el mero hecho de mirar un
  panel — más agresivo de lo que este proyecto quiere ser por defecto.
  Se reporta la antigüedad de la caché en su lugar y se deja al operador
  decidir su propia cadencia de actualización de caché.
- **Comparación de versiones por cadena de texto simple.** Descartada:
  produce resultados incorrectos de forma predecible (p. ej. "1.9" vs
  "1.10" mal ordenado), y es precisamente el tipo de bug silencioso que
  este proyecto ha evitado en todo lo demás.

## Consecuencias

### Positivas
- apt cubre el caso más común (iCloudServer, AlmaLinux aparte) sin
  privilegio adicional ni un solo `exec`.
- Se detecta el desfase real de versión con la misma lógica que usa el
  propio gestor de paquetes, no una aproximación.

### Negativas
- Cuatro fuentes con cuatro niveles de dificultad y de fiabilidad
  distintos — no hay "un colector de actualizaciones", hay cuatro, y el
  de dnf necesita un segundo helper privilegiado además del de SMART.
- Los datos de plugins de UnRaid y de imágenes Docker dependen de un
  tercero (la fuente del plugin, el registro de la imagen) que puede no
  responder, tener límite de tasa, o requerir credenciales — capacidad
  degradada real, no garantizada.
- El resultado de apt/dnf puede estar desactualizado si el operador no
  refresca su caché con la frecuencia que este proyecto asume razonable
  — se comunica, no se oculta.

## Notas de implementación

- Igual que el resto de inventarios de ADR-0015: `Inventory` de
  `kind: package_update`, un elemento por paquete/plugin/imagen
  desactualizado detectado, no por cada uno instalado — un sistema al día
  no necesita una lista de cientos de entradas "sin cambios".
- La comparación de versiones de dpkg/rpm no se reimplementa desde cero:
  existen implementaciones Go ya maduras del algoritmo de comparación de
  cada uno; se evalúa cuál añadir como dependencia al implementar, no se
  fija aquí.
- El helper de dnf sigue exactamente el mismo patrón que
  `cmd/bitacora-smart`: root, sin red, vida corta, temporizador — no un
  proceso persistente nuevo.
