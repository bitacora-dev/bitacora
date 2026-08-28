# ADR-0012: Sistema de solo lectura

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

La tentación es evidente y aparecerá pronto: si el sistema ya está en las cuatro
máquinas, con un agente en cada una y un panel central, ¿por qué no permitir
reiniciar un contenedor, relanzar un backup o aplicar actualizaciones desde la
interfaz?

Porque en el momento en que el hub puede ejecutar cosas en cuatro máquinas, deja
de ser una herramienta de observabilidad y pasa a ser un sistema de gestión
remota con privilegios. Las consecuencias son concretas:

- El agente necesita privilegios elevados de forma permanente, lo que anula todo
  el modelo del ADR-0005.
- El hub, que sirve HTML y expone una API web, se convierte en el objetivo más
  valioso de la red.
- La superficie de auditoría crece de "¿qué lee?" a "¿qué puede ejecutar y quién
  lo autorizó?".
- Existe un riesgo específico y grave: el sistema procesa **entrada no confiable**
  (nombres de contenedor, líneas de log, rutas, salida de comandos). Un sistema
  que además ejecuta acciones abre la puerta a que un dato observado dispare una
  acción.

## Decisión

**El sistema es de solo lectura.** Ni el agente ni el hub ejecutan acciones que
modifiquen el estado de las máquinas observadas.

Concretamente, queda prohibido:

- Ejecutar comandos arbitrarios, en cualquier forma.
- Cualquier tipo de shell o terminal remota.
- Arrancar, parar o reiniciar servicios, contenedores o unidades.
- Modificar ficheros fuera de `/var/lib/bitacora` y `/etc/bitacora`.
- Instalar o actualizar paquetes.
- Cualquier acción derivada de datos observados, sin excepción.

Los helpers privilegiados (ADR-0005) ejecutan **una lista cerrada de comandos de
consulta**, definida en el código, sin parámetros procedentes de datos externos.
`smartctl --json -a /dev/nvme0` es aceptable porque el dispositivo procede de
enumerar `/sys/block`, no de una petición.

### Si en el futuro se quieren acciones

Requerirá un ADR nuevo que sustituya a este, y como mínimo:

1. **Lista blanca explícita y corta**, definida en el fichero de configuración
   del agente, no en el hub. La máquina decide qué se le puede pedir.
2. Cada acción es una **operación con nombre, sin parámetros libres**
   (`restart-container:<nombre-de-la-lista>`), nunca una cadena de comando.
3. **Confirmación humana obligatoria** en la interfaz. Nunca automática, nunca
   disparada por una alerta.
4. **Registro de auditoría inmutable**: quién, qué, cuándo, desde dónde.
5. Segundo factor o token separado para el conjunto de acciones.
6. Deshabilitado por defecto.

Nada de esto forma parte del MVP ni del roadmap comprometido.

## Alternativas consideradas

- **Acciones limitadas desde el principio** (reiniciar contenedor, relanzar
  job). Descartado para el MVP. Es el 10 % del valor y el 80 % del riesgo de
  seguridad. El valor real está en entender qué pasa; ejecutar la corrección
  por SSH cuesta treinta segundos más y no compromete la arquitectura.
- **Remediación automática ante alertas.** Descartado con especial firmeza. Un
  sistema que reinicia servicios solo, ante una detección posiblemente errónea,
  puede convertir una degradación en una caída total. Y en el escenario que
  motiva el proyecto —un fallo de hardware intermitente— la remediación
  automática enmascararía el síntoma en lugar de exponerlo.
- **Integración con Ansible desde el hub.** Descartado: es exactamente el
  cambio de naturaleza que este ADR quiere impedir. Quien quiera Ansible, que
  use Ansible; son herramientas distintas y deben permanecer separadas.

## Consecuencias

### Positivas
- La superficie de ataque es mínima y auditable. Comprometer el hub da acceso a
  datos de monitorización, no control de cuatro máquinas.
- El agente puede correr sin privilegios (ADR-0005), lo que sería imposible con
  ejecución remota.
- El alcance del proyecto queda acotado, que en un proyecto de esta ambición es
  una virtud práctica, no solo de seguridad.
- Facilita la adopción por terceros: instalar un agente de solo lectura es una
  decisión mucho más fácil de justificar ante quien administra un servidor ajeno.

### Negativas
- Habrá que salir a un SSH para actuar. Se acepta conscientemente: el objetivo
  declarado era **no tener que entrar por SSH para entender qué pasa**, no para
  actuar.
- Es una limitación que se cuestionará repetidamente. Este ADR existe para no
  volver a discutirla cada vez sin argumentos nuevos.
- Alguien podría bifurcar el proyecto para añadir acciones. Es legítimo, y es lo
  que el open source permite.

## Notas de implementación

- El `README.md` debe declarar esto como característica, no como carencia:
  *"Bitácora es de solo lectura por diseño."* Es un argumento de venta ante quien
  valore la seguridad.
- CI debe incluir una comprobación que falle si aparece `os/exec` fuera de los
  paquetes de helpers y de `bitacora-run`. La restricción tiene que ser mecánica,
  no confiada a la revisión.
- El fichero `SECURITY.md` documenta el modelo de amenazas y este límite.
