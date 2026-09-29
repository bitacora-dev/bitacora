# ADR-0026: Canal de órdenes de recolección bajo demanda

- **Estado:** Propuesto
- **Fecha:** 2026-09-29

## Contexto

El 2026-09-29 el usuario pidió «un botón para actualizarlo cuando uno quiera».
Acababa de actualizar su servidor por SSH y el panel de actualizaciones seguía
mostrando 19 paquetes pendientes que ya no existían.

El colector `pkgupdates` corre cada 6 horas
(`cmd/bitacora-agent/main.go`). El intervalo es largo a propósito: de sus
cuatro fuentes solo apt es local. Los plugins de UnRaid y las imágenes Docker
hacen **una petición de red por item** contra la fuente declarada del plugin y
contra el registro de contenedores. Bajar el intervalo global multiplica de
forma permanente el tráfico que el host del usuario dirige a terceros, para
cubrir una necesidad que es ocasional.

ADR-0022 ya resolvió el caso automático adyacente y la tarea #1132 lo
implementó: tras un `apply` con éxito, el colector `packageactions` llama a
`collector.Runtime.RequestCollection("pkgupdates")` y el inventario se renueva
solo. Eso no cubre el caso reportado: el operador actualizó **fuera** de
Bitácora, por SSH o por Cockpit, y Bitácora no vio nada que disparar.

### Por qué no vale el botón que ya existe

El panel ya tiene un botón «refrescar»: `REFRESH_PACKAGE_CACHE`. Ejecuta
`apt update` como root, es una de las dos acciones que ADR-0022 autorizó como
excepción a ADR-0012, y por eso exige confirmación explícita y segundo factor.

Reescanear el inventario es otra cosa. El camino de apt de `pkgupdates` es
lectura pura de `/var/lib/dpkg/status` y `/var/lib/apt/lists` — sin `exec`, sin
privilegio, sin escritura en la máquina observada. Encajar esa lectura en el
vocabulario de acciones de ADR-0022 la obligaría a pasar por `actionconfirm` y
a pedir el TOTP. Acostumbrar al operador a teclear su segundo factor para una
operación inofensiva es exactamente cómo un segundo factor deja de significar
algo: cuando llegue la confirmación que sí importa, será el mismo gesto
reflejo.

### Por qué esto necesita un ADR

El hub no habla con el agente cuando quiere. El agente inicia
`POST /v1/ingest` y el hub responde; `IngestResponse` es el único camino
hub→agente que existe. Hoy lleva un solo tipo de orden,
`PendingPackageOperation`, tipado al enum `PackageOperation`, que el propio
`proto/ingest.proto` declara cerrado: *«It is the entire action vocabulary
ADR-0022 permits the hub to request from an agent»*.

Cualquier implementación del botón exige una de dos cosas: un valor nuevo en
ese enum cerrado, o un campo nuevo en `IngestResponse`. Las dos ensanchan el
contrato del canal de órdenes más allá de lo que ADR-0022 estableció. Por la
regla práctica de ADR-0000 —si revertir la decisión dentro de seis meses
costaría más de un día de trabajo, merece ADR— esto lo merece: toca el
protocolo, el almacenamiento del hub, la API del hub, el agente y la interfaz.

## Decisión

Se añade a `IngestResponse` un **segundo tipo de orden, separado y distinto**
del de ADR-0022: una solicitud de recolección bajo demanda.

```protobuf
// CollectorName is a closed set, like PackageOperation. It names collectors
// the hub may ask an agent to run early; it never carries an argument.
enum CollectorName {
  COLLECTOR_NAME_UNSPECIFIED = 0;
  PKGUPDATES = 1;
}

// PendingCollection asks the agent to run one named collector ahead of its
// schedule. It is a read, not an action: it changes nothing on the observed
// host and is therefore outside ADR-0022's action vocabulary.
message PendingCollection {
  CollectorName collector = 1;
  string request_id = 2;
  int64 expires_at_ms = 3;
}

message IngestResponse {
  string last_offset = 1;
  bool duplicate = 2;
  PendingPackageOperation pending_package_operation = 3;
  PendingCollection pending_collection = 4;
}
```

Reglas vinculantes:

1. **Es una lectura, no una acción.** Una orden de recolección no ejecuta
   comandos, no escribe en la máquina observada y no toca un helper
   privilegiado. El agente la sirve llamando a
   `collector.Runtime.RequestCollection(nombre)`, que es la capacidad que la
   tarea #1132 ya añadió. ADR-0012 sigue vigente sin excepción nueva: no se
   autoriza ninguna escritura que hoy no esté autorizada.
2. **Sin confirmación ni segundo factor.** La sesión humana autenticada de
   ADR-0019 basta. `actionconfirm`, sus tokens de un solo uso y la ceremonia de
   ADR-0022 **no** se aplican y no deben reutilizarse aquí. Una implementación
   que acabe pasando por `actionconfirm` está mal y debe pararse.
3. **Vocabulario cerrado, sin parámetros libres.** El protocolo transporta un
   valor de enum y un identificador de solicitud generado por el hub. No acepta
   nombres de paquete, rutas, cadenas de comando ni dato de inventario alguno.
   Un nombre de colector que el agente no conozca se rechaza y se registra.
4. **Solo desde una persona.** El hub crea una orden de recolección únicamente
   desde el control de la interfaz, con una sesión humana autenticada. Una
   alerta, una regla o un dato observado no pueden crearla. La barrera frente a
   entrada no confiable de ADR-0022 se aplica igual aquí.
5. **Intervalo mínimo entre reescaneos manuales: 15 minutos por host.** Lo
   impone el hub, no la interfaz: un botón deshabilitado en el navegador no es
   un límite. Mientras el intervalo no se haya cumplido, el hub rechaza la
   solicitud y devuelve el instante en que vuelve a estar disponible.
6. **La orden vence.** Como las de ADR-0022, lleva `expires_at_ms`. Un agente
   que estuvo tres días sin conexión no debe reescanear al reconectar por una
   orden que ya no interesa a nadie; el operador volverá a pulsar si le importa.
7. **Auditoría.** El hub registra un evento de solo anexado con identidad
   humana, host, colector, solicitud e instante, igual que para las órdenes de
   ADR-0022. Una orden es una orden aunque sea inofensiva.
8. **El agente conserva la iniciativa de red.** No se abre ningún puerto hacia
   el agente ni se añade streaming. Esto es lo mismo que decidió ADR-0022 y por
   las mismas razones: NAT, superficie de ataque y estados de conexión.

### Por qué 15 minutos

El colector programado corre 4 veces al día. Un intervalo mínimo de 15 minutos
acota el peor caso a 96 ciclos diarios adicionales si alguien se dedicara a
pulsar el botón sin parar, y en la práctica a los dos o tres de un operador que
acaba de actualizar y quiere ver el resultado. Es un techo del mismo orden de
magnitud que la cadencia programada, no varios órdenes por encima.

Es además lo bastante corto para el uso real —actualizo por SSH, pulso, veo el
resultado; si algo falló, corrijo y vuelvo a pulsar dentro de un cuarto de
hora— y lo bastante largo para que pulsarlo por aburrimiento no convierta el
host del usuario en un raspador de fuentes de plugins y registros de
contenedores ajenos. El coste de este botón no lo paga solo quien lo pulsa: lo
pagan terceros que no han consentido nada.

El valor se declara en el hub y se publica a la interfaz junto con el instante
del próximo reescaneo permitido, para que el control pueda explicar por qué
está deshabilitado. Un botón muerto sin explicación es peor que no tenerlo.

### Latencia y lo que la interfaz promete

El agente sondea; la orden viaja en la respuesta a su siguiente ingesta. Con la
cadencia normal, el reescaneo tarda como máximo un intervalo de ingesta en
empezar, y después un ciclo completo de `pkgupdates` —hasta su timeout de 2
minutos— en producir inventario nuevo.

La interfaz no puede fingir lo contrario. Se aplica el criterio que
`PackageUpdatePanel` ya estableció para las órdenes de ADR-0022: un estado
`pending` honesto mientras la orden está aceptada y el host no la ha recogido,
sin animación y sin inferir un inicio que no ha ocurrido. Si el host lleva sin
reportar más de lo que su cadencia de ingesta justifica, la interfaz lo dice con
palabras en vez de dejar girar una rueda indefinidamente.

## Alternativas consideradas

- **Añadir un tercer valor a `PackageOperation`.** Descartada. Mete una lectura
  pura en el vocabulario que ADR-0022 declaró cerrado y la arrastra por
  `actionconfirm`, con confirmación y segundo factor. Además de la fricción
  inútil, erosiona el segundo factor: entrena al operador a teclear el TOTP por
  reflejo para algo que no cambia nada.
- **Reutilizar `REFRESH_PACKAGE_CACHE`.** Descartada. Ejecuta `apt update` como
  root. Es una operación distinta, con consecuencias reales, y confundir las dos
  en un mismo control es precisamente el error que este ADR existe para evitar.
- **Bajar el intervalo global de `pkgupdates`.** Descartada. Multiplica de forma
  permanente las peticiones a fuentes de plugins y registros de contenedores
  para cubrir una necesidad ocasional, y sigue sin dar respuesta inmediata: solo
  acorta la espera.
- **Conformarse con el refresco automático tras `apply` (#1132).** Descartada
  como solución completa. Ya está implementado y es correcto, pero solo cubre
  las actualizaciones aplicadas desde Bitácora. El caso reportado es justo el
  contrario: el operador actualizó por SSH y Bitácora no vio nada.
- **Abrir un canal hub→agente (SSE, WebSocket o puerto en el agente).**
  Descartada por las mismas razones que ADR-0022: rompe la iniciativa de red del
  agente, deja de funcionar tras NAT y añade superficie y estados de conexión a
  un producto que hoy sirve JSON plano.
- **No hacer nada y esperar hasta 6 horas.** Descartada: es el defecto
  reportado. Un panel que afirma 19 actualizaciones pendientes que no existen no
  es un panel desactualizado, es un panel que miente.

## Consecuencias

### Positivas

- El operador ve el estado real cuando lo necesita, sin esperar seis horas y sin
  segundo factor para una lectura.
- Los dos refrescos quedan separados y distinguibles: `apt update` conserva
  intacta la ceremonia de ADR-0022; reescanear el inventario no la hereda.
- El límite de 15 minutos protege a terceros de un botón que, por diseño, gasta
  peticiones de red en fuentes que no son del usuario.
- Reutiliza `RequestCollection`, que ya existe, en vez de abrir un segundo
  mecanismo de recolección fuera de cadencia.

### Negativas

- **La frontera del canal cambia de forma.** Hasta hoy la regla era simple: el
  hub solo puede pedirle al agente dos acciones con nombre, ambas confirmadas
  por una persona con segundo factor. Pasa a ser: dos acciones con nombre más un
  conjunto cerrado de lecturas con nombre sin segundo factor. Es más difícil de
  explicar y más fácil de ensanchar la próxima vez; el enum `CollectorName`
  tiene un valor hoy y la presión para añadir el segundo llegará.
- Más código en un camino sensible: campo nuevo en el protocolo, tabla y
  limpieza de órdenes caducadas en el hub, endpoint nuevo, manejo nuevo en el
  agente. Todo eso hay que revisarlo, probarlo y mantenerlo.
- El límite por host es estado del hub. Debe sobrevivir a un reinicio o
  derivarse del registro de auditoría; si se guarda solo en memoria, reiniciar
  el hub elimina el límite en el momento menos oportuno.
- El botón sigue sin ser instantáneo. El operador verá un estado de espera y
  algunos lo reportarán como «el botón no hace nada». Es el precio de no abrir
  un canal persistente, y ADR-0022 ya lo aceptó para su propio caso.
- Un reescaneo manual gasta peticiones contra fuentes de plugins y registros
  ajenos. El límite lo acota, no lo elimina.

## Notas de implementación

Estas notas fijan el contrato de la tarea que implemente el botón, para que no
tenga que volver a decidir nada de lo anterior.

- **Dónde vive el control.** En `PackageUpdatePanel`, separado y visualmente
  distinguible del botón `REFRESH_PACKAGE_CACHE`. Los dos no pueden parecer el
  mismo control ni compartir copia. El de reescaneo es visible siempre que haya
  inventario; el de `apt update` conserva sus condiciones actuales.
- **Endpoint del hub.** `POST /v1/collections/requests`, con la sesión humana de
  ADR-0019 y alcance por servidor de ADR-0023. Devuelve la solicitud creada, o
  el rechazo por intervalo mínimo con el instante en que vuelve a permitirse.
- **Estado en la interfaz.** Cuatro estados, todos con texto:
  1. disponible;
  2. deshabilitado por intervalo mínimo, diciendo cuándo vuelve;
  3. aceptado y pendiente de que el host lo recoja — el estado honesto de
     `PackageUpdatePanel.tsx`, sin animación;
  4. host sin reportar, con mensaje explícito en vez de espera indefinida.
- **Fin de la espera.** El estado pendiente termina cuando llega un inventario
  `package_update` con `reported_at` posterior a la solicitud. No se infiere de
  un temporizador ni de un contador de reintentos.
- **Sin `Job`.** Una recolección no crea un `Job` de ADR-0010: no hay ejecución
  privilegiada que auditar línea a línea, y un `Job` sin salida real sugeriría
  un progreso que no existe. La auditoría es el evento del punto 7.
- **Cadencia de sondeo.** No se reduce el intervalo de ingesta por una orden de
  recolección pendiente. El acortamiento a 5 segundos de ADR-0022 existe para
  que «clic y empieza» se perciba inmediato en una acción privilegiada; una
  lectura no lo justifica y multiplicaría el sondeo por el botón más barato.
- **Cadenas en `es` y `en`**, y `web/DESIGN.md` vinculante, como en cualquier
  cambio de interfaz. Reconstruir `internal/webui/dist/` (ADR-0021).
