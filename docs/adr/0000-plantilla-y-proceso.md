# ADR-0000: Plantilla y proceso de ADR

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El proyecto va a ser implementado en buena parte por agentes de IA (Claude Code,
Codex CLI) trabajando en ramas `task/*`. Los agentes no comparten memoria entre
sesiones y tienden a re-decidir cosas ya decididas si no encuentran el rastro
escrito. Además el proyecto es público desde el primer commit, por lo que los
contribuidores externos necesitan entender *por qué* las cosas son como son sin
tener que preguntar.

## Decisión

Toda decisión de arquitectura se documenta como ADR en `docs/adr/`, numerado
secuencialmente, con este esqueleto:

```markdown
# ADR-XXXX: Título en una línea

- **Estado:** Propuesto | Aceptado | Rechazado | Superseded by ADR-YYYY
- **Fecha:** AAAA-MM-DD
- **Sustituye a:** (opcional)

## Contexto
Qué problema hay y qué restricciones aplican. Hechos, no opiniones.

## Decisión
Qué se hace. En presente e imperativo: "el agente escribe...", no "el agente
escribiría...".

## Alternativas consideradas
Cada una con el motivo real del descarte.

## Consecuencias
### Positivas
### Negativas
Las negativas son obligatorias. Un ADR sin consecuencias negativas no está
pensado, está vendido.

## Notas de implementación
Detalles que el implementador necesita y que no caben en el código.
```

Reglas de proceso:

- Un ADR = una decisión. Si un documento decide tres cosas, son tres ADR.
- Los ADR aceptados no se editan salvo erratas. Se sustituyen.
- El estado `Propuesto` significa **no implementar todavía**.
- Los agentes de IA leen `docs/adr/` como primer paso de cualquier tarea. Si la
  tarea contradice un ADR aceptado, el agente para y lo reporta.

## Alternativas consideradas

- **Documentación de arquitectura en un único `ARCHITECTURE.md`.** Se descarta:
  un documento vivo se reescribe y se pierde el histórico de por qué algo cambió,
  que es exactamente la información valiosa.
- **Wiki de GitHub.** Se descarta: no versiona junto al código, no pasa por PR y
  no está disponible para un agente que solo tiene el checkout del repo.
- **Solo issues.** Se descarta: los issues se cierran y se pierden en el ruido.

## Consecuencias

### Positivas
- Los agentes de IA tienen una fuente de verdad estable y localizable.
- Las revisiones cruzadas (Claude implementa, Codex audita) tienen contra qué
  contrastar.
- Un contribuidor externo entiende el diseño sin abrir una discusión.

### Negativas
- Fricción: cada decisión relevante cuesta escribir un documento.
- Riesgo de ADR-itis: documentar decisiones triviales. Regla práctica: si
  revertir la decisión dentro de seis meses costaría más de un día de trabajo,
  merece ADR.
