# ADR-0027: Alta de agentes y ciclo de vida del token de ingesta

- **Estado:** Propuesto
- **Fecha:** 2026-11-18
- **Amplía a:** ADR-0008 (no lo sustituye: todo lo que decide sigue vigente)

> **Nota de proceso.** La PR que introduce este ADR trae también su
> implementación de referencia. `Propuesto` describe el estado de la decisión,
> no el del código: quien revise la PR decide si la acepta —y entonces cambia
> el estado a `Aceptado`— o si pide cambios. El motivo de traerlos juntos es
> que la decisión solo se entiende contra el comportamiento concreto, y el
> comportamiento no se puede revisar contra una decisión que no está escrita.

## Contexto

ADR-0008 decide que hay **un token por agente**, vinculado a un `host_id`,
almacenado solo como hash Argon2id, generado con `bita agent create <nombre>`,
entregado una sola vez, y **revocable y rotable sin reinstalar el agente**.

De eso, antes de esta decisión, el árbol tenía la mitad: el hash Argon2id, el
vínculo con `host_id` y el rechazo de un lote cuyo `host_id` no coincide. No
tenía ni `bita agent`, ni revocación, ni rotación: `ingest_tokens` guardaba
`(host_id, token_hash, created_at)` y la única forma de dar de alta un agente
era `bitacora-hub -add-token <host_id>:<token>` o `POST /v1/hosts` desde la web.
Un token comprometido no se podía invalidar sin editar SQLite a mano, y nadie
podía responder «qué agentes hay dados de alta y desde cuándo».

Queda además un problema que ADR-0008 no aborda y que es el que más cuesta en
la práctica: el token en claro existe en el hub y tiene que terminar en
`/etc/bitacora/token` de otra máquina. Hoy eso es un copia-y-pega manual, y el
camino manual es el que acaba dejando el secreto en el historial del shell, en
un portapapeles, o en el canal por el que se mandó.

## Decisión

### Ciclo de vida del token

`ingest_tokens` gana `label`, `last_seen_at`, `revoked_at`, `expires_at` y
`rotated_from`. La migración se aplica leyendo el esquema vivo
(`PRAGMA table_info`) y añadiendo solo las columnas que faltan, de modo que es
re-ejecutable y compatible con un `tokens.db` existente: toda columna nueva es
anulable o tiene valor por omisión, y `revoked_at IS NULL` significa
exactamente «activo», que es el estado en que estaban los tokens anteriores.

Una credencial **no se borra nunca**. Revocar escribe `revoked_at`, y la fila
sobrevive: la pregunta de auditoría «¿este token fue válido, y hasta cuándo?»
tiene que poder responderse después de la revocación.

El camino de ingesta excluye las credenciales revocadas y caducadas **en la
propia consulta**. No hay caché de tokens verificados en ninguna parte, así que
una revocación surte efecto en la petición siguiente. Si alguna vez se añade
una caché para ahorrar el Argon2id por petición, su TTL tiene que ser lo
bastante corto para que «revocado» y «rechazado» sigan siendo lo mismo, y
revocar tiene que invalidarla.

`last_seen_at` se escribe como máximo una vez por minuto y por credencial. Cada
petición autenticada es candidata a una escritura —cada diez segundos por
agente, según ADR-0008—, y convertir un camino de solo lectura en carga de
escritura sostenida para responder algo que nadie pregunta al segundo no sale a
cuenta. Una escritura fallida de `last_seen_at` nunca decide el resultado de la
autenticación.

### Rotación con ventana de gracia

Rotar mina una credencial nueva y retira las anteriores con una **ventana de
gracia de 15 minutos por omisión**, durante la cual la vieja sigue valiendo.
Rotar existe para no reinstalar el agente, y eso implica un hueco entre minar
el reemplazo e instalarlo en la máquina; sin ventana, rotar costaría justo la
pérdida de datos que el buffer de ADR-0008 evita.

La ventana es un plazo, no una debilidad: quien crea que un token está
comprometido pide gracia cero (`-grace 0`, `grace_seconds: 0`) y la credencial
anterior queda revocada antes de que la respuesta se escriba.

La retirada de las viejas y la inserción de la nueva comparten transacción. Un
fallo no puede dejar un host sin credencial utilizable ni con dos vivas.

### Alta por código de un solo uso

Se añade un segundo tipo de credencial, de vida corta y distinta del token de
ingesta: el **código de alta**.

- El hub lo emite en `POST /v1/enrollments`, que exige una sesión humana con
  permiso `operate` (ADR-0023) **o** el token de dispositivo que `POST /v1/hosts`
  ya pedía. El token de dispositivo se mantiene para no romper los clientes de
  «Añadir servidor» que ya existen.
- El código es aleatorio de 160 bits, se guarda **solo como hash Argon2id**,
  caduca en 15 minutos, es de un solo uso y puede ir fijado a una etiqueta y,
  opcionalmente, a un `host_id` esperado.
- La máquina nueva lo canjea en `POST /v1/enroll` y recibe su `host_id` y su
  token de ingesta. Es la **única ruta de escritura sin autenticar** del hub, y
  por eso: presupuesto por IP acotado antes de hacer cualquier trabajo,
  comparación en tiempo constante, un único error genérico para todo fallo, y
  consumo atómico en la base de datos (`WHERE consumed_at IS NULL`), no en Go.
- Un código desconocido, uno caducado y uno ya canjeado responden lo mismo. Si
  se distinguieran, probar códigos sería un oráculo de cuáles existen.
- `bitacora-agent enroll` lo canjea y escribe el token en `/etc/bitacora/token`
  con modo `0600`, negándose a sobrescribir uno existente salvo `--force`.
  Es un paso de instalación que ejecuta el operador una vez; el demonio no lo
  ejecuta nunca y sigue sin escribir fuera de `/var/lib/bitacora` (ADR-0005).

El código no sustituye al token: lo sustituye al **transporte manual** del
token. El secreto que se pasea entre máquinas pasa a ser de un solo uso y a
caducar en 15 minutos.

### `bita agent`

`bita agent create|list|revoke|rotate` opera **sobre el `tokens.db` del hub,
no sobre su API**. Tiene que funcionar con el hub caído o antes de que exista
un dispositivo emparejado, que es justo cuando más falta hace mirar o
reemplazar una credencial. El fichero pertenece al usuario sin privilegios del
hub (ADR-0005), así que el operador lo ejecuta como ese usuario
(`sudo -u bitacora bita agent ...`). ADR-0012 no se toca: nada de esto ejecuta
nada, abre un fichero SQLite.

`tokens.db` y sus ficheros WAL se restringen a `0600` al abrirlos. Contienen
todos los hashes de la instalación y SQLite los crea con `0644` menos el
umask.

## Alternativas consideradas

- **Revocar borrando la fila.** Se descarta: deja la auditoría sin respuesta y
  hace indistinguible «se revocó» de «nunca existió».
- **Rotación sin ventana de gracia.** Se descarta como comportamiento por
  omisión: obliga a instalar el token nuevo en el mismo instante en que se mina,
  y mientras no está instalado el agente no ingiere. Se conserva como opción
  explícita, que es lo que hace falta ante una sospecha de compromiso.
- **Ventana de gracia larga (horas o un día).** Se descarta: deja viva una
  credencial superada durante una tarde entera, y el motivo normal para rotar es
  precisamente dejar de confiar en la anterior.
- **Caché de tokens verificados en el camino de ingesta.** Se descarta en esta
  decisión: ahorra un Argon2id por petición y a cambio le da al agente revocado
  una ventana en la que todavía ingiere. Con cuatro agentes (ADR-0004) el ahorro
  no compra nada.
- **Que `bita agent` hable con la API del hub.** Se descarta: necesitaría un
  token de dispositivo y un hub en pie, y el escenario de uso incluye no tener
  ninguno de los dos.
- **Alta por mTLS o por certificado de la máquina.** Se descarta: ADR-0008 ya
  descartó mTLS por la carga operativa de gestionar una CA, y nada de lo que
  aquí se decide cambia ese cálculo.
- **Código de alta corto, estilo el de emparejamiento por QR de ADR-0014.** Se
  descarta: aquel es corto porque una persona lo lee de una pantalla, y lo
  protege un TTL de cinco minutos y un solo uso. Un código de alta concede
  capacidad de **escritura permanente** sobre un host, así que no se le concede
  ese canje.
- **Tomar el `host_id` que presente la máquina sin más.** Se descarta: un código
  fijado a un `host_id` no puede dejar que quien lo presente redirija el alta a
  un identificador de su elección.
- **Entregar el código solo por consola local, como los tokens de ADR-0025.**
  Se descarta: ADR-0025 protege la **única identidad humana** de la instalación,
  donde la posesión local es la frontera entera. Aquí la frontera ya la probó
  quien pidió el código —sesión humana con `operate`, o token de dispositivo—, y
  obligar a acceso administrativo local al hub para dar de alta un servidor
  devolvería el flujo al SSH que esta decisión existe para quitar.

## Consecuencias

### Positivas

- La promesa de ADR-0008 —revocar y rotar sin reinstalar— pasa a existir.
- Un token comprometido se corta con un comando, y la siguiente petición del
  agente ya se rechaza.
- El secreto que viaja entre máquinas es de un solo uso y caduca en 15 minutos.
- `bita agent list` responde qué agentes hay, desde cuándo y cuándo se les oyó
  por última vez, que es la pregunta que antes no tenía respuesta.
- Nada de esto reinstala un agente ni toca su buffer: un agente revocado sigue
  acumulando localmente y, al rotarlo, recupera el atraso en vez de perderlo.

### Negativas

- Hay una ruta de escritura sin autenticar en el hub. Está acotada por el
  presupuesto por IP, el uso único, la caducidad y el error genérico, pero
  existe, y es superficie nueva que antes no estaba.
- La ventana de gracia deja viva una credencial superada hasta quince minutos.
  Es un plazo elegido, no un descuido, y se puede pedir cero.
- Dos tipos de credencial de vida corta conviven en el hub —el código de
  emparejamiento de ADR-0014 y el código de alta— con parámetros distintos por
  motivos distintos. Es una fuente real de confusión al leer el código, y por
  eso cada constante dice en su comentario por qué no vale la del otro.
- `bita agent` abre el mismo SQLite que el hub tiene abierto. WAL lo soporta,
  pero es un segundo escritor sobre el fichero y hay que contarlo.
- Las filas revocadas no se purgan nunca: `ingest_tokens` crece de forma
  monótona. Con el parque que ADR-0004 contempla es irrelevante; una instalación
  mucho mayor necesitaría una política de retención, que esta decisión no fija.

## Notas de implementación

- La migración lee `PRAGMA table_info` en vez de llevar un número de versión:
  un `tokens.db` escrito por cualquier build anterior converge a la misma forma,
  y abrirlo dos veces es inocuo.
- El nombre (`label`) es la clave con la que el operador se refiere a un agente;
  `host_id` es un ULID que nadie recuerda. `bita agent` rechaza crear dos
  agentes con el mismo nombre y rechaza actuar sobre un nombre ambiguo, en vez
  de adivinar cuál.
- `GET /v1/hosts` añade `status`, `created_at`, `token_last_seen_at` y `revoked`
  de forma **aditiva**: los campos que el frontend ya leía no se mueven, así que
  el contrato existente no cambia y no hace falta reconstruir
  `internal/webui/dist` (ADR-0021).
- Prefiere `-code -` y pasar el código por una tubería. Un código en un
  argumento queda en el historial del shell y en `/proc/<pid>/cmdline`, que
  cualquier usuario de la máquina puede leer.
- La instalación debe dejar `/etc/bitacora/token` como `root:bitacora` con modo
  `0600`. `bitacora-agent enroll` fija el modo; el propietario lo fija el paso
  de instalación, porque el demonio no corre como root (ADR-0005).
