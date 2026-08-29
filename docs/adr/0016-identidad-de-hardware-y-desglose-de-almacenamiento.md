# ADR-0016: Identidad de hardware y desglose de almacenamiento

- **Estado:** Aceptado
- **Fecha:** 2026-08-29

## Contexto

Tras ver el propio panel nativo de UnRaid (ADR-0015), quedan tres preguntas
sin resolver que no son "estado de salud" sino "qué máquina es esta y cómo
está compuesto su almacenamiento":

1. ¿Qué placa base, BIOS y procesador tiene cada servidor físico? El VPS
   queda fuera por definición — es virtualizado, no hay hardware real detrás
   que identificar (`sec.selinux`/`public.exposed` ya distinguen ese caso).
2. iCloudServer tiene 32 CPU lógicas, pero 2 llevan desactivadas desde una
   prueba diagnóstica tras el incidente que motiva ADR-0011 (offline
   deliberado de la CPU 8/9, el mismo P-core que concentraba los segfaults).
   Hoy no hay forma de ver en el panel que solo 30 de 32 están activas.
3. El disco/array de un servidor se muestra hoy como un único porcentaje
   global. En un array tipo SnapRAID/mergerfs (a diferencia de un RAID por
   bandas clásico) los discos no se llenan de forma uniforme — un 60% global
   puede ser un disco al 95% y otro al 20%. Y una carpeta compartida no dice
   cuánto ocupa ella sola.

## Decisión

**Las tres áreas siguen la misma norma que ADR-0015: solo lectura, sin
excepción.**

### 1. Identidad de hardware

Nuevo `Inventory` de `kind: hardware_identity`, un elemento por servidor
físico (nunca en el VPS):

- Placa base: fabricante, modelo, versión — de `/sys/class/dmi/id/board_*`.
- BIOS: fabricante, versión, fecha — de `/sys/class/dmi/id/bios_*`.
- Procesador: modelo — de `/proc/cpuinfo`.
- Consumo del paquete en vatios — de Intel RAPL
  (`/sys/class/powercap/intel-rapl:0/energy_uj`, delta entre dos lecturas).
  **Nota de privilegio:** desde la mitigación del CVE-2020-8694, varios
  kernels restringen la lectura de los contadores RAPL a root. Puede
  necesitar un helper privilegiado nuevo (patrón `bitacora-smart`,
  ADR-0005) — se confirma al implementar, no se asume aquí.

Todo lectura directa de fichero, sin `exec`, mismo patrón que el resto del
proyecto. Explícitamente fuera de esta ADR: la RAM máxima soportada por la
placa (Type 17 SMBIOS) — requiere decodificar tablas DMI crudas, más trabajo
del que justifica el valor frente al resto de esta ampliación.

### 2. Topología de CPU: qué núcleos existen y cuáles están activos

`internal/faultcluster.Topology` ya lee, para ADR-0011, el mapeo CPU
lógica → core físico, el estado online/offline (`OfflineCPUs()`) y la
clasificación P-core/E-core en CPUs híbridas Intel. No hace falta
construirlo de nuevo: se expone lo que ya existe como `Inventory` de
`kind: cpu_topology` — una fila por CPU lógica con su core físico, tipo
(P-core/E-core/desconocido) y si está online.

Esto es exactamente el caso de iCloudServer: 32 filas, 30 `online: true`,
2 `online: false` — visibles en el panel sin necesidad de SSH, que es la
razón por la que se detectó el patrón del incidente en primer lugar.

### 3. Desglose de almacenamiento: por disco, no solo global

**Por disco del array:** un `statfs` por cada punto de montaje miembro del
array (capacidad, usado, disponible) — una syscall por disco, sin coste
real. Se combina con lo que `bitacora-smart` ya lee por dispositivo
(ADR-0005): la salida de `smartctl --json` trae de serie el modelo, el
número de serie, el identificador WWN y el path del dispositivo
(`/dev/sdX`, `/dev/nvmeXnX`) además de temperatura y salud — no hay que
construir nada nuevo para identificar cada disco, solo dejar de
descartarlo al mostrarlo. Cada disco del array se muestra con esa
identificación completa, su propio porcentaje de uso, temperatura y
salud SMART — igual que ya lo enseña UnRaid (ver captura de referencia:
tabla con dispositivo, identificación modelo+serie+WWN, temperatura,
lecturas/escrituras, errores, sistema de ficheros, capacidad/usado/
disponible, una fila por disco), y consistente con que un array
SnapRAID/mergerfs no reparte los datos de forma uniforme entre discos.
Aplica igual a un pool de caché o a cualquier grupo de almacenamiento
adicional (ADR-0004 ya distingue `storage.mdraid` de `storage.unraid_array`
de `storage.mergerfs`; esta ADR no añade un cuarto tipo de array, solo
dice que **cualquiera de ellos** se desglosa por disco miembro, no solo el
array principal).

**Por carpeta compartida:** tamaño ocupado por cada compartido (ADR-0015).
A diferencia de todo lo anterior, esto **no es barato**: no existe un
contador de "cuánto ocupa este directorio" en el sistema de ficheros — hay
que recorrer el árbol completo (equivalente a `du -sh`), que en una carpeta
de medios de varios TB puede tardar minutos. Se calcula como un job
periódico de baja frecuencia (patrón temporizador de `bitacora-smart`, no
el camino de 1 Hz de la caja negra), con el tamaño quedando "a fecha de la
última pasada", no en vivo. Se muestra la fecha de ese cálculo junto al
dato, para que quede claro que no es instantáneo.

## Alternativas consideradas

- **Ejecutar los backups/syncs bajo demanda desde el panel.** Pedido en la
  misma conversación que originó esta ADR, y descartado explícitamente
  aquí: contradice ADR-0012 de forma directa ("ninguna acción derivada de
  datos observados, sin excepción"), que exige un ADR propio que lo
  sustituya, con lista blanca, confirmación humana y auditoría, si
  alguna vez se quiere. No se decide en esta ADR — queda pendiente de una
  conversación aparte, consciente, si se retoma.
- **`du` en cada ciclo de recolección para el tamaño de compartidos.**
  Descartado: convertiría cada colección en una operación de minutos sobre
  arrays grandes, incompatible con cualquier cadencia razonable. De ahí el
  job periódico de baja frecuencia.
- **Decodificar las tablas SMBIOS completas (Type 17) para la RAM máxima
  soportada.** Descartado para esta ADR por relación esfuerzo/valor — es
  el único dato de la captura original de UnRaid que se deja fuera.

## Consecuencias

### Positivas
- La topología de CPU reutiliza código ya escrito y probado
  (`internal/faultcluster`) — coste marginal, no una superficie nueva.
- El desglose por disco convierte "60% usado" en información accionable
  ("el disco 4 está a punto de llenarse, los demás no") — justo lo que
  un array no uniforme necesita y un porcentaje global oculta.
- Nada de esto requiere abrir la puerta de las acciones (ADR-0012 intacto).

### Negativas
- RAPL puede no estar disponible sin privilegio adicional según el kernel
  — capacidad degradada real, no garantizada en todos los hosts.
- El tamaño por compartido es un dato con fecha de caducidad implícita: si
  el job de cálculo lleva un día sin correr, lo que se muestra ya no es
  cierto. Hay que comunicarlo en la UI (fecha del último cálculo), no
  presentarlo como si fuera en vivo.
- Un `statfs` por disco del array es barato individualmente, pero un array
  con muchos discos (iCloudServer, 17 en el ejemplo de UnRaid visto) suma
  17 syscalls por ciclo — trivial en términos absolutos, pero es
  información nueva que sumar a cada colección.

## Notas de implementación

- `hardware_identity` y `cpu_topology` son `Inventory` de solo una entrada
  activa por host (no crecen con el tiempo, se sobrescriben en cada envío,
  igual que el manifiesto de capacidades).
- El colector de topología de CPU es una envoltura fina sobre
  `internal/faultcluster.ReadTopology` — no reimplementar la lectura de
  `/sys/devices/system/cpu`.
- El job de tamaño por compartido necesita su propio intervalo configurable
  (por defecto sugerido: una vez al día, fuera de horas de uso), y debe
  poder desactivarse por compartido si un operador lo considera demasiado
  caro para uno concreto (p. ej. un volumen de decenas de TB).
