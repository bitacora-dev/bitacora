# Architecture Decision Records — Bitácora

Este directorio contiene las decisiones de arquitectura del proyecto. Cada ADR
documenta **una** decisión: su contexto, las alternativas descartadas y sus
consecuencias, incluidas las malas.

## Reglas

1. Los ADR son **inmutables**. No se editan tras aceptarse. Si una decisión
   cambia, se escribe un ADR nuevo que la sustituye y se marca el anterior como
   `Superseded by ADR-XXXX`.
2. Todo cambio de arquitectura relevante empieza con un ADR en estado `Propuesto`
   y una PR contra `development`.
3. Los agentes de IA (Claude Code, Codex CLI) **deben** leer este directorio
   antes de implementar. Si una tarea contradice un ADR, se detiene la tarea y se
   abre un ADR nuevo — no se improvisa.
4. Ningún agente hace merge. El merge lo hace una persona.

## Índice

| ADR | Título | Estado |
|-----|--------|--------|
| [0000](0000-plantilla-y-proceso.md) | Plantilla y proceso de ADR | Aceptado |
| [0001](0001-lenguajes-y-stack.md) | Lenguajes y stack tecnológico | Aceptado |
| [0002](0002-separacion-agente-hub.md) | Separación agente / hub | Aceptado |
| [0003](0003-almacenamiento-tres-capas.md) | Almacenamiento en tres capas | Aceptado |
| [0004](0004-multihost-y-manifiesto-de-capacidades.md) | Multi-host y manifiesto de capacidades | Aceptado |
| [0005](0005-modelo-de-privilegios.md) | Modelo de privilegios y helpers | Aceptado |
| [0006](0006-esquema-canonico-de-datos.md) | Esquema canónico de métricas, eventos y logs | Aceptado |
| [0007](0007-interfaz-collector.md) | Interfaz `Collector` y ciclo de vida | Aceptado |
| [0008](0008-transporte-autenticacion-y-buffer.md) | Transporte, autenticación y buffer local | Aceptado |
| [0009](0009-motor-de-alertas.md) | Motor de alertas y notificación | Aceptado |
| [0010](0010-modelo-job-y-bitacora-run.md) | Modelo `Job` y wrapper `bitacora-run` | Aceptado |
| [0011](0011-caja-negra-y-diagnostico-de-cuelgues.md) | Caja negra y diagnóstico de cuelgues | Aceptado |
| [0012](0012-solo-lectura.md) | Sistema de solo lectura | Aceptado |
| [0013](0013-nombre-licencia-y-gobernanza.md) | Nombre, licencia y gobernanza | Aceptado |
| [0014](0014-clientes-y-notificaciones.md) | Clientes nativos y notificaciones push | Aceptado |
| [0015](0015-ampliacion-de-superficie.md) | Ampliación de superficie: compartidos, VMs, usuarios, red y alimentación | Aceptado |

## Estado del proyecto

- **Nombre:** Bitácora · organización `bitacora-dev` · repositorio `bitacora`
- **Licencia:** AGPL-3.0 (núcleo) y Apache-2.0 (`bitacora-run`) — ver ADR-0013
- **Registro de contenedores:** `ghcr.io/bitacora-dev/`
- **Idioma:** repositorio en inglés, ADR en español — ver ADR-0013

## Estados

- **Propuesto** — en discusión, no implementar todavía.
- **Aceptado** — vinculante. El código debe cumplirlo.
- **Rechazado** — se documenta para no volver a discutirlo.
- **Superseded** — reemplazado por otro ADR, que se cita.
