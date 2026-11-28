# ADR-0022: Actualizaciones de paquetes con confirmación humana

- **Estado:** Propuesto
- **Fecha:** 2026-09-19
- **Sustituye a:** parcialmente, ADR-0012 (solo para las acciones de refrescar la caché de paquetes y aplicar actualizaciones de paquetes)

## Contexto

El usuario ha pedido aplicar las actualizaciones pendientes desde Bitácora y
ver una ejecución que se sienta viva. En iCloudServer la caché de apt tiene una
antigüedad observada de 75.756.467 segundos (aproximadamente 2,4 años). Aplicar
actualizaciones contra esa caché sería actuar sobre una decisión obsoleta.

ADR-0012 prohíbe instalar o actualizar paquetes y cualquier acción derivada de
datos observados. Esa prohibición sigue siendo la regla general porque Bitácora
procesa entrada no confiable: nombres de contenedor, líneas de log, rutas y
salida de comandos. Si esos datos pudieran convertirse en una orden, un dato
observado podría ejecutar una acción.

La topología actual tampoco tiene canal hub→agente: el agente inicia un `POST`
de ingesta y el hub responde. `IngestResponse` solo contiene `last_offset` y
`duplicate`; no hay streaming, SSE ni WebSocket. ADR-0010 ya define `Job` y sus
líneas de log para representar una ejecución larga. Cockpit ya está instalado
en la máquina afectada y ofrece actualización, descarga y registro en vivo.

## Decisión

Se autoriza **solo** una excepción acotada a ADR-0012: un operador humano puede
solicitar, en un host que lo habilite expresamente, estas operaciones sin
parámetros libres:

1. `refresh-package-cache`
2. `apply-pending-package-updates`

No se autoriza reiniciar servicios, contenedores o unidades; ejecutar comandos
arbitrarios; ni ninguna otra escritura. ADR-0012 continúa vigente para todo lo
demás, incluidos sus límites de shell, de ficheros y de acciones derivadas de
observaciones.

### Requisitos de ADR-0012 para acciones

1. **Lista blanca explícita y corta.** El fichero de configuración local del
   agente declara, por host, si permite cada una de las dos operaciones. El hub
   no puede ampliarla ni habilitarla. Ambas quedan deshabilitadas por defecto.
2. **Operación con nombre, sin parámetros libres.** El protocolo transporta
   únicamente uno de esos dos nombres y un identificador de solicitud generado
   por el hub. El agente mapea cada nombre a su implementación fija; no acepta
   argumentos, rutas, nombres de paquetes, cadenas de comando ni datos de
   inventario.
3. **Confirmación humana obligatoria.** La interfaz muestra host, operación,
   motivo, instantánea de paquetes y consecuencias. Una alerta, una regla o un
   dato observado no pueden crear, confirmar ni reenviar una orden. Solo la
   confirmación explícita de una persona autenticada crea una solicitud.
4. **Registro de auditoría inmutable.** Antes de entregar la orden, el hub
   añade un evento de solo anexado y sin API de modificación o borrado que
   contiene identidad humana, operación, host, solicitud, instante, origen de
   red y resultado. El agente añade el inicio, las transiciones y el resultado
   con la misma solicitud; la retención y los permisos impiden que el usuario
   de ejecución los altere.
5. **Segundo factor o token separado.** La confirmación exige un token de
   acciones distinto de la sesión web, de los tokens de dispositivos y de los
   tokens de ingesta. Es de un solo uso, tiene vencimiento corto y está ligado a
   identidad, host, operación y solicitud. Sin autenticación humana aceptada y
   este segundo factor, la funcionalidad no se implementa ni se habilita.
6. **Deshabilitado por defecto.** La configuración inicial del agente no
   permite ninguna acción y la interfaz no ofrece el botón hasta que el
   operador habilita cada nombre localmente y configura el segundo factor.

### Barrera frente a entrada no confiable

El hub solo crea una orden desde el flujo de confirmación humana y con una
operación fija; nunca desde inventarios, logs, alertas, etiquetas ni texto que
haya recibido del agente. La instantánea mostrada sirve para informar, no para
construir argumentos. La orden queda ligada criptográficamente a su solicitud,
host y operación, vence antes de ejecutarse y el agente la rechaza si no
coincide con su lista blanca local o si ya fue consumida. Por tanto, incluso un
nombre de contenedor o una línea de log manipulados no tienen un camino de datos
hacia una acción ni pueden cambiar qué operación fija se ejecuta.

### Canal de órdenes y latencia

Las órdenes pendientes viajan en la respuesta de ingesta, ampliando
`IngestResponse`; no se abre un puerto hacia el agente. La conexión sigue
siendo iniciada por el agente, funciona tras NAT y conserva la frontera actual.

Con el intervalo normal de ingesta, el clic confirmado tarda como máximo un
intervalo en llegar al agente. Para hosts con acciones habilitadas, el agente
debe usar un intervalo máximo de 5 segundos mientras exista una orden
pendiente. Cinco segundos es el límite aceptado para que «clic y empieza» se
perciba inmediato sin introducir un canal persistente; si el host está sin
conexión, la interfaz muestra que la orden está pendiente y no simula inicio.

### Refresco de caché y aplicación

`refresh-package-cache` entra en el alcance precisamente porque una caché de
2,4 años hace insegura la selección de actualizaciones. Es una acción escrita,
no una lectura, y cumple los seis requisitos anteriores. Cuando la caché exceda
la edad máxima configurada, la interfaz exige confirmar primero el refresco,
esperar su resultado y volver a recoger la lista. Solo entonces muestra el plan
actualizado y exige una segunda confirmación y un segundo token para
`apply-pending-package-updates`. No se aplican paquetes elegidos desde una
instantánea caducada.

### Progreso en vivo

La ejecución crea un `Job` de ADR-0010 con estado `running`, líneas de salida y
referencias en `logstore`. La interfaz sondea el job y sus líneas cada pocos
segundos hasta un estado terminal. Se elige este modelo porque reutiliza la
auditoría, almacenamiento y vista de trabajos ya definidos, y no añade SSE ni
WebSocket a un producto que hoy sirve JSON plano. El sondeo tiene más latencia y
peticiones que streaming, pero es suficiente para mostrar inicio, descarga y
finalización de una operación administrativa poco frecuente.

## Alternativas consideradas

- **No hacerlo y usar Cockpit.** Cockpit ya cubre exactamente esta necesidad y
  mantiene la menor superficie de ataque para Bitácora. Sigue siendo la opción
  recomendada para quien no necesite correlacionar la actualización con la
  observabilidad de Bitácora. Se acepta duplicar el caso concreto porque el
  usuario ha pedido el flujo dentro del panel y porque `Job` permite conservar
  una trazabilidad común; el coste de seguridad y mantenimiento no desaparece.
- **Mantener ADR-0012 sin excepciones.** Es la opción más segura y coherente
  con el argumento de solo lectura, pero no satisface la decisión explícita del
  usuario. Se sustituye solo para estas dos operaciones, no como autorización
  general de gestión remota.
- **SSE o WebSocket para el progreso.** Descartado ahora: no existe streaming
  en el hub ni en el agente y añadirlo aumenta superficie, estados de conexión y
  operación. El sondeo de `Job` y `logstore` entrega la señal necesaria.
- **Refrescar la caché implícitamente al abrir el panel o al aplicar.**
  Descartado: convierte una lectura o una confirmación de aplicación en una
  escritura oculta. El refresco es una operación visible, auditada y confirmada.

## Consecuencias

### Positivas

- El operador puede refrescar metadatos y aplicar actualizaciones desde la misma
  vista que informa de su necesidad, con trazabilidad de principio a fin.
- El agente conserva la iniciativa de red y la operación se limita a dos
  nombres habilitados por la propia máquina.
- El progreso reutiliza `Job` y `logstore`, sin un segundo sistema de streaming.

### Negativas

- Bitácora deja de poder venderse sin matices como «Bitácora es de solo lectura
  por diseño», el argumento que ADR-0012 pedía al README. La comunicación debe
  pasar a explicar que es de lectura por defecto y que esta excepción requiere
  habilitación local, confirmación humana y segundo factor.
- El hub y el agente pasan a formar parte de una cadena de ejecución remota;
  una vulnerabilidad en ella tiene consecuencias sobre sistemas operados.
- Hay dos confirmaciones y dos tokens cuando la caché es antigua: es fricción
  deliberada frente al riesgo de aplicar una lista obsoleta.
- El sondeo no es streaming: el progreso tiene retraso, genera peticiones y no
  puede prometer actividad instantánea durante una caída de conectividad.
- Se duplica una capacidad que Cockpit ya ofrece y se asume su mantenimiento,
  pruebas de seguridad, auditoría y soporte futuro.

## Notas de implementación

- El trabajo posterior se divide en tareas bloqueadas por este ADR: autenticación
  humana y segundo factor, protocolo de solicitud firmada, configuración local
  del agente, helpers privilegiados de vida corta, auditoría inmutable y UI de
  jobs por sondeo. Ninguna de esas tareas puede convertir datos observados en
  argumentos u órdenes.
- Los helpers mantienen ADR-0005: el daemon no corre como root; cada operación
  privilegiada es efímera y de implementación fija.
- El README y `SECURITY.md` se actualizan en una tarea posterior al aprobar este
  ADR; este cambio de decisión no implementa ni habilita la funcionalidad.
