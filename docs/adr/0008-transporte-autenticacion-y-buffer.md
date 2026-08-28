# ADR-0008: Transporte, autenticación y buffer local

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

Los agentes envían datos al hub a través de una red que puede fallar. El hub
vive en iCloudServer, la máquina que precisamente ha sufrido un cuelgue duro. Uno
de los agentes correrá en un VPS expuesto a internet. Y todo el parque está en un
tailnet.

Perder muestras durante una caída del hub sería especialmente grave: los minutos
previos a un fallo son los que más importan.

## Decisión

### Transporte

- **HTTP/2 con cuerpo en Protobuf**, comprimido con zstd. No gRPC.
- Endpoint de ingesta: `POST /v1/ingest` con lotes mixtos (métricas, eventos,
  líneas de log) en un solo mensaje.
- Cadencia: cada 10 s, o antes si el lote supera 1 MB o si hay un evento de
  severidad `critical` (los eventos críticos se envían de inmediato, sin
  esperar al lote).
- El hub responde con el **último offset confirmado**. El agente no borra de su
  buffer hasta recibir confirmación.

### Autenticación

- **Un token por agente**, generado por el hub con `bita agent create
  <nombre>` y entregado una sola vez.
- El token va en `Authorization: Bearer`. En el hub se almacena solo su
  **hash Argon2id**, nunca el token.
- Cada token está **vinculado a un `host_id`**. El hub rechaza un lote cuyo
  `host_id` no coincida con el del token. Un agente comprometido no puede
  falsificar datos de otra máquina.
- Los tokens se pueden revocar y rotar sin reinstalar el agente.
- El fichero del token en el agente: `/etc/bitacora/token`, `0600`,
  `root:bitacora`.

### Red

- El transporte por defecto es **la interfaz de Tailscale**. El puerto de
  ingesta no escucha en `0.0.0.0`.
- TLS: si el tráfico va por el tailnet, ya está cifrado y autenticado a nivel de
  red; se acepta HTTP simple sobre esa interfaz. **Fuera del tailnet, TLS es
  obligatorio** y el agente rechaza conectar en claro a una dirección no
  loopback y no Tailscale.
- Se documentan ACLs de Tailscale recomendadas para que solo los agentes
  alcancen el puerto de ingesta.

### Buffer local del agente

WAL en disco en `/var/lib/bitacora/spool/outbound/`:

- Ficheros por segmentos de 4 MB, comprimidos.
- **Capacidad por defecto: 2 horas** o 256 MB, lo que llegue antes.
- Al superarse el límite, se descarta **por prioridad, no por antigüedad**:
  primero líneas de log, después métricas de resolución cruda, y **nunca**
  eventos ni jobs. Se emite un evento `agent.buffer_overflow` con lo descartado.
- Al reconectar, el agente hace **backfill** en orden cronológico, con límite de
  tasa para no saturar el hub al volver.
- El buffer sobrevive a un reinicio del agente y del host.

### Idempotencia

Cada lote lleva un ULID. El hub ignora lotes ya ingeridos. Esto hace que
reintentar sea seguro y que el backfill tras una reconexión no duplique datos.

## Alternativas consideradas

- **gRPC.** Descartado pese a ser la opción obvia: añade una dependencia
  considerable al binario del agente, complica el paso por proxies inversos y
  no aporta nada sobre HTTP/2 + Protobuf para un patrón de petición-respuesta
  por lotes. Se reconsideraría si hiciera falta streaming bidireccional.
- **JSON en lugar de Protobuf.** Descartado por volumen: JSON multiplica por 3-5
  el tamaño del lote y el coste de parseo. Se mantiene JSON solo en la API de
  lectura que consume el frontend, donde el volumen es pequeño y la depuración
  importa más.
- **mTLS con una CA propia.** Descartado para el MVP: gestión de certificados,
  rotación y revocación es una carga operativa alta para el beneficio marginal
  sobre tokens dentro de un tailnet. Reevaluable si aparecen despliegues fuera
  de Tailscale.
- **Sin buffer, aceptando pérdida durante cortes.** Descartado: perder los
  minutos previos a un fallo elimina la razón de ser del sistema.
- **Buffer en memoria únicamente.** Descartado: no sobrevive al reinicio del
  agente, que es justo lo que ocurre tras un cuelgue.
- **Cola externa (NATS, Redis).** Descartado: una dependencia más a instalar,
  contraria al principio de instalación autocontenida.

## Consecuencias

### Positivas
- Una caída del hub de hasta dos horas es transparente: al volver, el histórico
  está completo.
- El aislamiento por token limita el daño de un agente comprometido, lo que
  importa especialmente con el VPS expuesto.
- Sin puertos públicos abiertos en ninguna máquina.

### Negativas
- El buffer consume disco en el agente (hasta 256 MB) y hay que contarlo en el
  presupuesto de recursos.
- La política de descarte por prioridad es lógica adicional y una fuente
  potencial de bugs sutiles. Necesita test explícito de desbordamiento.
- La dependencia de Tailscale para el modelo de seguridad por defecto es una
  suposición sobre el entorno del usuario. Debe existir y estar documentado el
  camino con TLS propio para quien no lo use.
- Protobuf implica generación de código y mantener los `.proto` sincronizados.

## Notas de implementación

- Los `.proto` viven en `proto/` y se compilan en CI; el código generado se
  commitea para que compilar no requiera `protoc`.
- El agente expone `bitacora_agent_buffer_bytes` y
  `bitacora_agent_last_flush_timestamp`. Si el buffer crece de forma sostenida,
  es un síntoma de que el hub está degradado y debe generar alerta.
- **El agente nunca reintenta indefinidamente sin backoff**: exponencial con
  jitter, techo de 60 s. Con cuatro agentes reconectando a la vez tras un
  reinicio del hub, sin jitter llegan sincronizados.
- El endpoint de ingesta necesita límite de tasa por token, para que un agente
  con un bug no tumbe el hub.
