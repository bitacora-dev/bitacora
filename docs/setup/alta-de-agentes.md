# Alta de agentes y ciclo de vida del token

Implementa [ADR-0008](../adr/0008-transporte-autenticacion-y-buffer.md) y
[ADR-0027](../adr/0027-alta-de-agentes-y-ciclo-de-vida-del-token.md), con el
modelo de privilegios de [ADR-0005](../adr/0005-modelo-de-privilegios.md) y los
permisos por servidor de
[ADR-0023](../adr/0023-autenticacion-local-y-alcance-por-servidor.md).

Cada agente tiene **un token propio**, vinculado a su `host_id`. El hub guarda
solo su hash Argon2id: el token en claro se ve **una sola vez**, cuando se
emite. No hay comando, endpoint ni fichero que lo vuelva a mostrar. Si lo
pierdes, **rota**; no lo busques.

## Lo que conviene saber antes

| Hecho | Por qué importa |
| --- | --- |
| El token va vinculado a un `host_id` | El hub rechaza un lote cuyo `host_id` no coincida. Un agente comprometido no puede falsificar datos de otra máquina. |
| El hub guarda solo el hash | Un volcado de `tokens.db` no contiene nada que se le pueda presentar al hub. |
| Revocar surte efecto en la petición siguiente | No hay caché de tokens verificados en el camino de ingesta. |
| Revocar **no** borra nada | La fila sobrevive con su `revoked_at`, para que la auditoría pueda responder «fue válido hasta cuándo». |
| Un agente revocado **no pierde datos** | Sigue acumulando en su buffer local (hasta 2 h o 256 MB). Al rotarlo, recupera el atraso. |
| `tokens.db` pertenece al usuario `bitacora` | El hub no corre como root. Los comandos locales se ejecutan como ese usuario. |

## Camino recomendado: código de alta de un solo uso

Evita el copia-y-pega del token entre máquinas. Lo que viaja es un código que
**caduca en 15 minutos y vale una sola vez**.

### 1. Pide el código en el hub

Desde la interfaz web («Añadir servidor») o directamente contra la API, con una
sesión humana con permiso `operate` o con el token de dispositivo:

```bash
curl -s -X POST https://hub.tu-tailnet.ts.net/v1/enrollments \
  -H "Authorization: Bearer $BITACORA_DEVICE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"vps-berlin"}'
```

```json
{
  "code": "p9F3...",
  "label": "vps-berlin",
  "expires_at": "2026-11-18T13:25:07Z",
  "enroll_command": "bitacora-agent enroll -hub https://hub.tu-tailnet.ts.net -code -"
}
```

Si la máquina **ya tiene** un `host_id` (reinstalas el agente, no el sistema),
fija el código a ese identificador para que conserve su histórico:

```bash
  -d '{"name":"vps-berlin","host_id":"01JEXISTING..."}'
```

### 2. Canjéalo en la máquina nueva

```bash
# Como root, o como un usuario que pueda escribir /etc/bitacora.
read -rs CODE          # lo pegas aquí; no queda en el historial
printf '%s' "$CODE" | sudo bitacora-agent enroll \
  -hub https://hub.tu-tailnet.ts.net -code -
```

```
enrolled as 01JENROLLED...
  host_id: /var/lib/bitacora/host_id
  token:   /etc/bitacora/token (mode 0600)
```

El token **no se imprime**: ya está en disco. El comando escribe
`/etc/bitacora/token` con modo `0600` y persiste el `host_id` que el hub
asignó.

> **Pasa el código por una tubería, no como argumento.** `-code <CODE>` deja el
> secreto en el historial del shell y en `/proc/<pid>/cmdline`, que cualquier
> usuario de la máquina puede leer mientras el proceso vive. `-code -` (o
> ninguna de las dos, que también lee de la entrada estándar) lo evita. Si
> necesitas una variable, `BITACORA_ENROLL_CODE` sirve, pero el entorno de un
> proceso también es legible por su propietario.

### 3. Deja los permisos que pide la unidad y arranca

`bitacora-agent enroll` fija el **modo**; el **propietario** lo fijas tú,
porque el demonio no corre como root (ADR-0005) y no puede hacerlo por sí
mismo:

```bash
sudo chown root:bitacora /etc/bitacora/token
sudo systemctl enable --now bitacora-agent
```

### Flags de `bitacora-agent enroll`

| Flag | Por omisión | Qué hace |
| --- | --- | --- |
| `-hub URL` | `BITACORA_HUB_URL` | URL base del hub. Obligatoria. |
| `-code CODE` | `BITACORA_ENROLL_CODE` | El código. `-`, o vacía, lee de la entrada estándar. |
| `-token-file PATH` | `/etc/bitacora/token` | Dónde escribir el token. |
| `-host-id-path PATH` | `/var/lib/bitacora/host_id` | Dónde vive el `host_id` persistente. |
| `-force` | — | Sobrescribe un token existente. Sin esto, se niega. |

Sin `-force`, un token ya existente **no se sobrescribe**: un comando mal
teclado desconectaría un agente que funciona, y el token que reemplazó no se
puede recuperar. Un `host_id` distinto del que ya hay en disco tampoco se
reemplaza sin `-force`, porque eso desvincula la máquina de su propio
histórico.

## Camino local: `bita agent`

Opera sobre el `tokens.db` del hub, **no sobre su API**. Funciona con el hub
caído y antes de que exista ningún dispositivo emparejado, que es justo cuando
más falta hace mirar o reemplazar una credencial.

El fichero pertenece al usuario sin privilegios del hub, así que se ejecuta
como ese usuario:

```bash
sudo -u bitacora bita agent list
```

| Comando | Qué hace |
| --- | --- |
| `bita agent create <nombre> [-host-id ID]` | Mina una credencial y la imprime una sola vez. |
| `bita agent list` | Qué agentes hay, su estado, desde cuándo y cuándo se les oyó. |
| `bita agent revoke <nombre>` | Corta la credencial. La siguiente petición del agente se rechaza. |
| `bita agent rotate <nombre> [-grace D]` | Mina el reemplazo y retira la anterior. |

Todos aceptan `-data-dir` (por omisión `/var/lib/bitacora`), y las flags pueden
ir antes o después del nombre.

```
$ sudo -u bitacora bita agent list
NAME        HOST_ID                     STATUS    CREATED               LAST SEEN
vps-berlin  01JHZ8...                   active    2026-11-18T09:12:00Z  2026-11-18T13:05:00Z
nas         01JHZA...                   rotating  2026-11-18T09:14:00Z  2026-11-18T13:05:12Z
viejo-vps   01JH9Q...                   revoked   2026-10-02T11:40:00Z  2026-11-01T08:21:00Z

1 enrollment code(s) issued and not yet redeemed
```

- `rotating` — se rotó y la credencial anterior aún está dentro de su ventana
  de gracia.
- `expired` — la ventana se agotó sin que se instalara el reemplazo. El agente
  está callado y sabes por qué.
- `(unnamed)` — credencial creada con `bitacora-hub -add-token` o heredada de
  un `tokens.db` anterior a ADR-0027. Sigue valiendo; rótala para ponerle
  nombre.

### Rotar

```bash
# Por omisión: la credencial anterior sigue valiendo 15 minutos.
sudo -u bitacora bita agent rotate vps-berlin

# Si crees que el token está comprometido: muere ya.
sudo -u bitacora bita agent rotate vps-berlin -grace 0
```

La ventana de gracia existe porque rotar **no reinstala el agente**: hay un
hueco entre minar el reemplazo e instalarlo, y durante ese hueco el agente
tiene que seguir enviando. Instala el token nuevo antes de que se agote:

```bash
printf '%s\n' "$NUEVO_TOKEN" | sudo install -m 0600 -o root -g bitacora /dev/stdin /etc/bitacora/token
sudo systemctl restart bitacora-agent
```

## Desde la API

| Endpoint | Auth | Qué hace |
| --- | --- | --- |
| `POST /v1/enrollments` | sesión humana con `operate`, o token de dispositivo | Emite un código de alta de un solo uso. |
| `POST /v1/enroll` | solo el código | Lo canjea por `host_id` + token. Límite de tasa por IP. |
| `GET /v1/hosts` | token de dispositivo o sesión humana | Incluye `status`, `created_at`, `token_last_seen_at` y `revoked`. |
| `POST /v1/hosts/{id}/revoke` | igual que el alta | Revoca las credenciales del host. |
| `POST /v1/hosts/{id}/rotate` | igual que el alta | Mina el reemplazo. `{"grace_seconds":0}` para corte inmediato. |

`POST /v1/hosts` sigue existiendo y sin cambios en su contrato: emite el token
directamente, sin código intermedio.

Una persona con sesión necesita `operate` **sobre ese host**: ver un servidor
no autoriza a cortarle la telemetría (ADR-0023), y un host que no puede operar
se le responde como inexistente, no como prohibido.

## Modelo de amenaza

**Fuerza bruta del código.** 160 bits de entropía, caducidad de 15 minutos, un
solo uso y presupuesto por IP en `POST /v1/enroll` (dos intentos de golpe,
luego uno cada diez segundos). El presupuesto se cuenta por dirección, no por
puerto de origen: abrir una conexión nueva no lo reinicia.

**Enumeración.** Un código desconocido, uno caducado y uno ya canjeado
responden exactamente lo mismo. No hay forma de distinguirlos, ni de saber si
un `host_id` existe.

**Reutilización.** El consumo es atómico en la base de datos: dos peticiones
con el mismo código válido compiten y gana exactamente una. Un código
presentado con un `host_id` que contradice el que tiene fijado se consume
igualmente, para que no sirva como oráculo de identificadores.

**El código en el historial.** Pásalo por una tubería (`-code -`). Un argumento
queda en el historial del shell y en la lista de procesos.

**El token en tránsito.** Con el código de alta, el token en claro solo existe
en la respuesta de `POST /v1/enroll` y en el fichero `0600` que el comando
escribe. Nunca pasa por un portapapeles ni por un canal de mensajería.

**Red.** Nada de esto sustituye a ADR-0008: el puerto de ingesta no escucha en
`0.0.0.0`, y fuera del tailnet TLS es obligatorio. Un código de alta por HTTP
en claro fuera del tailnet es un código leído por quien esté en medio.

## Qué hacer si

| Situación | Qué hacer |
| --- | --- |
| Perdiste el token de un agente | `bita agent rotate <nombre>`. No se puede recuperar. |
| Crees que un token está comprometido | `bita agent rotate <nombre> -grace 0`, instala el nuevo y reinicia el agente. |
| Retiras una máquina | `bita agent revoke <nombre>`. La fila queda, con su fecha de revocación. |
| Un código caducó sin usarse | Pide otro. El anterior ya no vale para nada. |
| `no token store at ...` | `-data-dir` no apunta al directorio del hub, o el hub no ha arrancado nunca. |
| `cannot read ... permission denied` | Te falta ejecutarlo como el usuario del hub: `sudo -u bitacora`. |
