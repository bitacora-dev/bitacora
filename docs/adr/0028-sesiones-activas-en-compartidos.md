# ADR-0028: Sesiones activas en compartidos

- **Estado:** Propuesto
- **Fecha:** 2026-11-27
- **Sustituye parcialmente a:** [ADR-0015: Ampliación de superficie](0015-ampliacion-de-superficie.md), solo su no-objetivo sobre conexiones SMB/NFS vivas.

## Contexto

ADR-0015 incorpora el inventario estático de compartidos SMB/NFS, pero excluye
sus conexiones vivas: el inventario responde qué compartidos existen, no quién
los está usando ahora. El panel denso necesita una columna **Conexiones** por
carpeta para responder esa segunda pregunta, como hace Unraid.

La fuente SMB soportada para ese dato es `smbstatus --json`. Consultarla exige
ejecutar un binario y, por tanto, no puede hacerlo el agente sin contradecir el
modelo de solo lectura de ADR-0012. La alternativa de leer directamente los
TDB de Samba bajo `/var/lib/samba/` evita ejecutar un proceso, pero depende de
formatos internos, versiones y configuración de Samba que Bitácora no
controla. Para NFS, `/proc/fs/nfsd/clients/*` y `/var/lib/nfs/rmtab` pueden
aportar evidencia sin ejecutar comandos, pero su disponibilidad y permisos
varían entre distribuciones.

El dashboard se comparte con más frecuencia que el servidor. Tal como exige
`web/DESIGN.md` para cuentas y permisos, una identidad de usuario o una IP de
cliente no debe aparecer por defecto. Además, una muestra cada minuto no es
una conexión «en vivo»: debe exponer su antigüedad. Una ausencia de helper o
de capacidad tampoco es un recuento cero.

## Decisión

Se permite observar sesiones SMB mediante un helper privilegiado específico,
de vida corta y con una lista de ejecución cerrada, siguiendo ADR-0005 y sin
abrir ejecución genérica en el agente ni en el hub.

1. El paquete instala `bitacora-shares.service` de tipo `oneshot` y un
   `bitacora-shares.timer` con cadencia de un minuto. El helper ejecuta solo
   `smbstatus --json`, sin argumentos procedentes de la red, el hub, la
   configuración dinámica ni datos observados.
2. El helper se ejecuta aislado como root, sin red, con tiempo máximo y
   permisos mínimos; escribe de forma atómica un JSON versionado en
   `/var/lib/bitacora/spool/`. El agente no se eleva: valida el esquema y la
   antigüedad antes de consumir el fichero, como establece ADR-0005.
3. El JSON emitido contiene, por defecto, únicamente el recuento de
   conexiones por identificador de compartido y su instante de muestreo. No
   transporta ni almacena usuario, IP, nombre de cliente ni otra identidad.
   Una futura divulgación de esos datos requiere un ADR y una configuración
   explícita; no se habilita implícitamente por instalar el helper.
4. Para NFS, el agente intenta solo lectura de `/proc/fs/nfsd/clients/*` y
   `/var/lib/nfs/rmtab` cuando sean accesibles para `bitacora`. Antes de
   publicar soporte se verifica esa accesibilidad por distribución y se
   documenta la semántica exacta de cada fuente. Si no son legibles o no
   permiten atribuir con seguridad una sesión a un compartido, NFS queda **no
   reportado**; no se amplían privilegios ni se añade `exec` para compensarlo.
5. La interfaz muestra el recuento junto con la edad de la muestra y lo
   presenta como dato muestreado, no en tiempo real. Cuando el helper no está
   instalado, falla, está obsoleto o una fuente NFS no está disponible, muestra
   «no reportado», nunca `0`.
6. La excepción de `os/exec` queda limitada al helper de compartidos y al
   comando fijo anterior. `scripts/ci/check-no-exec.sh` se actualiza con esa
   única lista blanca; el resto de los paquetes conserva la prohibición de
   ADR-0012.
7. El coste se evalúa contra el RSS y CPU globales del agente según ADR-0024.
   No se atribuye una porción de RSS inventada al collector: se mide el
   proceso ensamblado y se informa el coste adicional real del parseo y de la
   lectura del spool.

## Alternativas consideradas

- **Mantener las sesiones vivas fuera de alcance.** Descartado: deja sin
  respuesta una señal operativa que el panel denso necesita para administrar
  compartidos y obliga a abrir SSH o la consola de Samba.
- **Leer los TDB internos de Samba.** Descartado: elimina `exec`, pero acopla
  el producto a formatos no estables ni versionados como API. Una actualización
  de Samba puede convertir un dato aparentemente válido en una lectura
  incorrecta y silenciosa.
- **Ejecutar `smbstatus` desde el agente permanente.** Descartado: extendería
  la superficie de ejecución de un proceso que tiene red y procesa entradas no
  confiables; contradice ADR-0005 y ADR-0012.
- **Emitir usuario e IP de cliente por defecto.** Descartado: son datos más
  sensibles que el recuento y el panel se comparte más que el servidor.
- **Mostrar cero cuando falta la fuente.** Descartado: cero afirma una
  medición; una fuente no instalada, inaccesible o obsoleta no mide nada.
- **Tratar el timer de un minuto como tiempo real.** Descartado: cada valor
  puede tener casi un minuto de retraso, además del tiempo de transporte y
  refresco de la interfaz.

## Consecuencias

### Positivas

- El panel recibe un recuento útil por carpeta sin convertir Bitácora en una
  herramienta de administración remota.
- El privilegio queda limitado a un proceso corto, auditable, sin red y con un
  único comando fijo, conforme a ADR-0005 y ADR-0012.
- La privacidad por defecto evita propagar identidades de clientes por la ruta
  agente, transporte, hub y navegador.
- La distinción entre cero y «no reportado» evita decisiones operativas basadas
  en ausencia de datos.

### Negativas

- Añade una unidad, un timer, empaquetado, mantenimiento de lista blanca y una
  nueva ruta de JSON que validar.
- SMB deja de ser estrictamente lectura de configuración estática; depende de
  una herramienta instalada y de su salida JSON.
- Una cadencia de un minuto tiene retraso y puede omitir sesiones muy breves.
- NFS puede permanecer no reportado en parte de las distribuciones hasta que
  se prueben permisos y semántica de atribución por compartido.
- El helper y el parseo aumentan el coste operativo, que debe permanecer dentro
  del presupuesto global de ADR-0024.

## Notas de implementación

Las tareas derivadas son:

1. Definir el esquema canónico versionado para recuentos de sesiones SMB/NFS,
   con `reported_at`, estado de disponibilidad y ausencia explícita de
   identidades de clientes.
2. Implementar la lectura, validación de antigüedad y combinación del spool
   con el inventario de `internal/collector/shares`, sin convertir ausencia en
   cero.
3. Crear y empaquetar el helper, servicio y timer de compartidos con el
   endurecimiento de ADR-0005; añadir la excepción mínima y comprobada a
   `scripts/ci/check-no-exec.sh`.
4. Investigar por distribución los permisos y la semántica de
   `/proc/fs/nfsd/clients/*` y `/var/lib/nfs/rmtab`; implementar NFS solo donde
   se pueda asociar el dato a un compartido sin elevar privilegios.
5. Transportar y persistir el nuevo atributo hasta el hub, preservando su
   instante de muestreo y la distinción entre valor, error y no reportado.
6. Añadir la columna «Conexiones» al panel denso, su antigüedad, traducciones y
   el estado «no reportado»; mantener usuario e IP fuera de la UI.
7. Añadir pruebas de JSON inválido/obsoleto, datos ausentes, privacidad,
   correlación con compartidos, empaquetado y presupuesto de recursos global.
