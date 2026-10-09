# ADR-0020: Distribución del agente Linux con soporte de journald

- **Estado:** Aceptado
- **Fecha:** 2026-09-07

## Contexto

El lector real de journald usa `sdjournal`, que requiere CGO y las cabeceras de
`libsystemd`. Cuando el agente se compila sin CGO entra deliberadamente el
fallback que deshabilita el colector. El agente desplegado actualmente se
compiló de ese modo y, por tanto, nunca ha recogido logs de journald.

ADR-0001 decide distribuir binarios estáticos y prohíbe por defecto las
dependencias CGO en el agente. Pedir a cada host observado que instale un
compilador y `libsystemd-dev` traslada complejidad de construcción al operador
y rompe esa propiedad de distribución.

## Decisión

Para Linux amd64, CI construye y publica un artefacto separado de
`bitacora-agent` con `CGO_ENABLED=1` y `libsystemd-dev`. Ese es el artefacto
que se instala en hosts systemd que deben recoger journald. El proceso de
instalación verifica que el binario no contiene la cadena del fallback
`requires cgo and libsystemd` antes de sustituir el binario en producción.

El agente continúa ejecutándose como el usuario no privilegiado `bitacora` y
mantiene el sandbox de ADR-0005; CGO no cambia su modelo de privilegios.

Los artefactos sin CGO siguen siendo la variante para destinos que no usan
journald o que no son Linux. El empaquetado `.deb` y `.rpm` posterior debe
seleccionar explícitamente la variante Linux con journald, no reconstruir en
el host destino.

## Alternativas consideradas

- **Construir en cada host destino.** Descartado: obliga a instalar un
  compilador y cabeceras de desarrollo en cada máquina observada y hace que el
  resultado dependa del host.
- **Mantener un único binario sin CGO.** Descartado: deshabilita el colector de
  journald y deja la canalización de logs sin fuente.
- **Usar `journalctl -o json`.** Descartado: requeriría ejecutar un comando
  desde el agente permanente, contrario a ADR-0012.

## Consecuencias

### Positivas

- El artefacto desplegable reproduce la compilación que habilita journald sin
  toolchain en los hosts observados.
- La comprobación con `strings` detecta la inclusión accidental del fallback,
  incluso cuando `ldd` no puede hacerlo por el uso de `dlopen`.

### Negativas

- Linux con journald deja de ser un binario completamente estático.
- CI debe mantener `libsystemd-dev` y publicar artefactos por arquitectura.
- Los operadores deben elegir el artefacto correcto para su plataforma.

## Notas de implementación

- La publicación debe conservar los logs de compilación y la salida de la
  comprobación `strings` como evidencia del artefacto.
- La migración de un despliegue existente debe detener el proceso antiguo antes
  de habilitar la unidad, y debe corregir el token existente a
  `root:bitacora 0640` con `/etc/bitacora` en modo `0750`.
