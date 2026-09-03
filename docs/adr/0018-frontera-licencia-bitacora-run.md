# ADR-0018: Frontera de licencia de bitacora-run

- **Estado:** Propuesto
- **Fecha:** 2026-09-03

## Contexto

ADR-0013 decide que el núcleo de Bitácora (`bitacora-hub`,
`bitacora-agent`, `bita` y el frontend) queda bajo AGPL-3.0, mientras
`bitacora-run` queda bajo Apache-2.0. También exige que cada directorio
con licencia distinta tenga su propio `LICENSE` y cabeceras claras.

Esa decisión no nombraba los paquetes Go que `bitacora-run` necesita para
compilar. A fecha de esta propuesta, `cmd/bitacora-run` importa:

- `internal/execwrap`
- `internal/jobwriter`
- `internal/runstats`
- `internal/schema`

Además, `internal/jobwriter` importa `internal/spool`. Si esos paquetes se
leen como AGPL-3.0 por herencia del `LICENSE` de la raíz, `bitacora-run` no
es realmente Apache-2.0: su binario queda arrastrado por una dependencia
interna AGPL. Eso contradice el objetivo explícito de ADR-0013.

## Decisión propuesta

La frontera práctica es esta:

| Ruta | Licencia | Motivo |
|---|---|---|
| `cmd/bitacora-run` | Apache-2.0 | Es el componente que ADR-0013 declara permisivo para maximizar adopción. |
| `internal/execwrap` | Apache-2.0 | Es soporte directo de `bitacora-run`; no depende del núcleo AGPL y encapsula la ejecución permitida por ADR-0010/ADR-0012. |
| `internal/runstats` | Apache-2.0 | Sus extractores son funciones puras sobre la salida ya capturada por `bitacora-run`; no necesitan código del núcleo salvo el contrato de datos. |
| `internal/jobwriter` | Apache-2.0 | Es el adaptador mínimo para entregar un `schema.Job` al agente o al spool sin acoplar `bitacora-run` al núcleo. |
| `internal/spool` | Apache-2.0 | Es el formato de intercambio en disco entre helpers, agente y `bitacora-run`; hacerlo permisivo permite que el camino Apache escriba sin importar AGPL. |
| `internal/schema` | Apache-2.0 | Es el contrato de datos compartido, no una implementación del núcleo. El núcleo AGPL puede depender de código Apache; lo inverso no. |
| Resto del repositorio | AGPL-3.0 | Sigue siendo el núcleo protegido por ADR-0013. |

La regla operativa queda escrita así:

> El código bajo Apache-2.0 solo puede importar código bajo Apache-2.0,
> además de la biblioteca estándar y dependencias externas compatibles.

El sentido contrario sí está permitido: el núcleo AGPL puede importar paquetes
Apache-2.0 internos cuando esos paquetes son contratos o adaptadores de
interoperabilidad.

## Consecuencias

- Leer un fichero Go suelto en la frontera ya no requiere inferir la licencia
  desde la raíz: cada fichero lleva cabecera SPDX.
- Cada directorio Apache-2.0 tiene su propio `LICENSE`.
- CI falla si cualquier paquete Apache-2.0 importa un paquete interno que no
  esté marcado también como Apache-2.0.
- La decisión evita mover paquetes ahora. Moverlos a un árbol público
  (`pkg/` o similar) puede considerarse más adelante si se quiere que otros
  módulos los importen, pero no es necesario para cerrar la frontera de este
  repositorio.

## Decisiones pendientes

Esta propuesta materializa la lectura mínima necesaria para que ADR-0013 sea
ejecutable en el árbol actual. Si el mantenedor considera que `internal/schema`
o `internal/spool` no deben ser Apache-2.0, entonces `bitacora-run` debe dejar
de importarlos y obtener tipos/serialización propios bajo `cmd/bitacora-run`
o bajo un nuevo árbol Apache-2.0. Ese cambio es mayor y debe decidirlo una
persona antes de reescribir la frontera.
