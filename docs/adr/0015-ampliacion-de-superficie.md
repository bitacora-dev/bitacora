# ADR-0015: Ampliación de superficie — compartidos, VMs, usuarios, red y alimentación

- **Estado:** Aceptado
- **Fecha:** 2026-08-29

## Contexto

El diseño original (ADR-0004 en adelante) se centró en la salud del hardware y
del sistema operativo: CPU, memoria, disco, temperatura, procesos, kernel. Al
comparar el panel resultante contra el propio panel nativo de UnRaid, la
diferencia es evidente: UnRaid muestra también qué carpetas están
compartidas y con qué permisos, qué máquinas virtuales hay activas, qué
usuarios existen, cuánto tráfico cursa cada interfaz de red, qué túneles VPN
están levantados, y el estado del SAI si lo hay.

Ninguna de estas cinco cosas es "salud de hardware" en el sentido que
cubrían los ADR anteriores, pero todas son información que un administrador
consulta habitualmente sin querer abrir una sesión SSH — que es exactamente
el objetivo declarado del proyecto. Se decide ampliar la superficie
observada.

## Decisión

**Las cinco áreas siguientes entran en el alcance del proyecto, todas como
datos de solo lectura — ADR-0012 no se toca: se amplía qué se observa, nunca
si se puede actuar.**

1. **Compartidos (SMB/NFS).** Nombre, ruta, descripción si existe, y modo de
   permiso (público/privado, lectura/escritura). Leído de la configuración
   del servicio (`smb.conf`, `/etc/exports`, o el fichero de configuración de
   compartidos propio de UnRaid), no de una consulta al servicio en marcha.
2. **Máquinas virtuales.** Nombre, estado (`running`/`stopped`/`paused`) y
   asignación de recursos declarada (vCPUs, RAM). Vía el socket de solo
   lectura de libvirt, con el mismo patrón de pertenencia a grupo que
   `internal/collector/journald` ya usa para journald — sin CGO, sin
   necesidad de root.
3. **Usuarios.** Cuentas de sistema relevantes para los compartidos (excluye
   cuentas de servicio: UID por debajo del umbral de "usuario real" del
   sistema) y su permiso declarado de lectura/escritura por compartido.
4. **Red: tráfico por interfaz y túneles VPN.** Bytes/paquetes rx-tx por
   interfaz como serie temporal (`/proc/net/dev`, ya un patrón conocido en
   este proyecto — mismo tipo de lectura que `/proc/diskstats` en
   `internal/blackbox`). Estado de túneles VPN (Tailscale, WireGuard) como
   inventario: nombre del túnel, peer, si está activo, último handshake.
5. **Alimentación (SAI/UPS).** Estado de conexión, carga de batería,
   ¿en red eléctrica o en batería?, tiempo de autonomía restante. Vía NUT
   (`upsc`) o el fichero de estado de `apcupsd`, cuando el operador tiene uno
   configurado — capacidad degradada (ADR-0004) cuando no lo hay, no un
   error.

### Un cuarto tipo de dato: `Inventory`

ADR-0006 define tres formas de dato: `Metric` (serie temporal), `Event`
(hecho discreto) y `LogLine` (línea cruda). Ninguna encaja con "la lista de
compartidos que existen ahora mismo" — no es una serie temporal, no es un
suceso puntual. Es exactamente la misma forma que ya tiene el manifiesto de
capacidades (ADR-0004): una foto declarativa que se reenvía entera cada vez
que cambia, no en delta.

Se añade `Inventory` como cuarta forma canónica:

```json
{
  "host_id": "01J8X...",
  "kind": "share",
  "reported_at": "2026-08-29T20:00:00Z",
  "schema": 1,
  "items": [
    { "id": "multimedia", "name": "Multimedia", "attrs": { "path": "/mnt/user/multimedia", "mode": "private", "protocol": "smb" } }
  ]
}
```

`kind` es uno de `share`, `vm`, `user`, `vpn_tunnel`, `ups`. `attrs` es libre
por `kind`, mismo espíritu que `stats` en el modelo `Job` (ADR-0010): claves
canónicas cuando existen, libres cuando no. El tráfico de red **no** usa
`Inventory` — es una serie temporal real y usa `Metric`, etiquetada por
interfaz.

### Privilegio

Siguiendo ADR-0005, cada colector declara el mínimo privilegio real:

| Fuente | Acceso necesario |
|---|---|
| `smb.conf` / `/etc/exports` | Lectura de fichero, sin privilegio |
| `/etc/passwd` (usuarios) | Lectura de fichero, sin privilegio |
| libvirt (VMs) | Pertenencia al grupo `libvirt`, sin root |
| `/proc/net/dev` (tráfico) | Lectura de fichero, sin privilegio |
| Tailscale (estado del túnel) | Socket local de Tailscale, típicamente sin root |
| WireGuard (estado del túnel) | Requiere `CAP_NET_ADMIN` — helper privilegiado nuevo, patrón `bitacora-smart` |
| NUT / `apcupsd` | Socket de red local o fichero de estado, típicamente sin privilegio |

WireGuard es la única fuente de esta ampliación que necesita un helper
privilegiado nuevo. El resto se lee sin privilegios adicionales sobre lo que
ADR-0005 ya concede.

## Alternativas consideradas

- **Modelar compartidos/VMs/usuarios como `Event`.** Descartado: un `Event`
  es un suceso puntual con fingerprint y deduplicación; "el compartido
  Multimedia existe" no es un suceso, es un estado persistente. Forzarlo
  como evento habría significado reemitir el mismo evento sin cambios en
  cada ciclo, o inventar una lógica de "evento que nunca se resuelve".
- **Consultar los servicios en caliente en vez de su configuración estática**
  (p. ej. `smbstatus` para compartidos activos con conexiones reales).
  Descartado para la primera versión: requiere `exec` en más sitios de los
  que ADR-0012 permite hoy sin abrir helpers nuevos por cada servicio.
  Queda como mejora futura, no bloqueante — la configuración estática ya
  responde "¿qué existe?", que es la pregunta que motivó esta ampliación.
- **Un colector genérico "UnRaid" que replique su panel entero.** Descartado:
  volvería a la misma trampa que ADR-0004 ya rechazó — detección por
  plataforma en vez de por capacidad. Compartidos y VMs no son exclusivos de
  UnRaid; libvirt corre igual en AlmaLinux.
- **Tráfico de red como `Inventory` en vez de `Metric`.** Descartado: bytes
  por segundo es exactamente el caso de uso que `Metric` ya resuelve bien
  (series temporales, downsampling, gráficas). Forzarlo a `Inventory`
  habría perdido toda esa maquinaria sin motivo.

## Consecuencias

### Positivas
- El panel deja de mostrar solo "salud de hardware" y empieza a responder
  la pregunta real que motivó el proyecto: "¿qué está pasando en mis
  servidores, sin abrir una terminal?".
- `Inventory` es reutilizable para lo próximo que aparezca con esta misma
  forma (paquetes pendientes de actualizar, certificados por expirar) sin
  inventar un quinto tipo de dato cada vez.
- Compartidos, usuarios y VMs se recogen sin privilegios nuevos sobre lo que
  ADR-0005 ya concede, salvo WireGuard.

### Negativas
- **Usuarios y permisos rozan gestión de identidad**, no solo salud de
  hardware — es la pieza más alejada del alcance original del proyecto y
  la que más fricción puede generar si en el futuro se plantea ocultar
  datos sensibles por rol. No se resuelve aquí: se acepta el riesgo y se
  revisita si aparece un caso real de multi-usuario con necesidades de
  privacidad entre sí.
- **Cinco colectores nuevos son cinco superficies nuevas de mantenimiento**
  — formatos de configuración que cambian entre versiones de Samba, NFS,
  libvirt, Tailscale, WireGuard, NUT/apcupsd. Cada uno necesita su propia
  disciplina de "mejor esfuerzo, degrada con gracia" ya establecida en el
  resto del proyecto.
- **WireGuard requiere un helper privilegiado nuevo**, el segundo tras
  `bitacora-smart` — más superficie sujeta al modelo de ADR-0005.
- La configuración estática de compartidos (en vez de conexiones activas en
  tiempo real) significa que el panel puede mostrar un compartido que
  técnicamente nadie está usando ahora mismo — es un inventario, no una
  sesión activa. Aceptado: resolver eso es la alternativa descartada de
  arriba.

## Notas de implementación

- `internal/inventory` como paquete nuevo: el tipo `Inventory` (ADR-0006
  extendido), y el transporte de subida agente→hub necesita un mensaje
  nuevo en `proto/bitacorapb` — igual que Job (ADR-0010) necesitó el suyo,
  que sigue pendiente de esa misma extensión.
- Cada colector nuevo sigue la interfaz `Collector` de ADR-0007 sin
  excepción — ninguno de los cinco necesita el camino separado que sí
  justifica la caja negra (ADR-0011).
- El manifiesto de capacidades (ADR-0004) gana capacidades nuevas:
  `share.smb`, `share.nfs`, `vm.libvirt`, `net.wireguard`, `net.tailscale`
  (ya existe para otro propósito, reutilizar), `power.ups`.
- La UI necesita una pantalla nueva por `kind` de `Inventory`, no una
  extensión de `GET /v1/summary` — mezclar listas de tamaño variable con
  series temporales de tamaño fijo en la misma respuesta rompe la promesa
  de ADR-0014 de "una sola llamada, una sola pantalla". Se decide un
  endpoint propio, `GET /v1/inventory?host_id=...&kind=...`.
- Formatos exactos de fichero de configuración (rutas concretas de
  `smb.conf` en cada distribución, forma exacta del estado de UnRaid,
  versión mínima de libvirt) se verifican contra un host real al
  implementar cada colector — no se asumen aquí.
