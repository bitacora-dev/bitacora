# ADR-0002: Separación agente / hub

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El despliegue inicial es una sola máquina (iCloudServer), pero el objetivo
declarado incluye al menos cuatro: iCloudServer (Ubuntu), un AlmaLinux, un
UnRaid y un VPS en OVH. Además, la máquina que alojará el hub es precisamente la
que ha sufrido un cuelgue duro reciente.

## Decisión

Dos binarios independientes desde el primer commit, comunicados **siempre por
red**, incluso cuando corren en la misma máquina:

- **`bitacora-agent`** — recolecta. No almacena histórico (salvo su buffer de
  reenvío). No expone UI. No conoce a los demás agentes. Empuja al hub.
- **`bitacora-hub`** — ingiere, almacena, evalúa alertas, sirve API y UI.

En el despliegue de una sola máquina, el agente habla con el hub por
`127.0.0.1:8081`. No existe un modo "todo en proceso": eliminarlo evita que el
código desarrolle dependencias implícitas entre ambos lados.

El sentido de la comunicación es **push desde el agente**, no pull desde el hub.

## Alternativas consideradas

- **Binario único monolítico, con multi-host añadido después.** Descartado. Es la
  decisión que parece ahorrar trabajo hoy y cuesta una reescritura mañana: cada
  collector acabaría accediendo al almacenamiento directamente, y separar eso
  después implica tocar todo el código y el modelo de datos entero. El
  sobrecoste de separar ahora es de un 20-25 %; hacerlo después multiplica por
  dos o tres.
- **Modelo pull tipo Prometheus** (el hub raspa `/metrics` de cada agente).
  Descartado por tres motivos: (a) obliga a que cada agente escuche en un puerto
  alcanzable desde el hub, lo que en un VPS es superficie de ataque; (b) durante
  un corte de red se pierden las muestras, mientras que en push el agente las
  bufferiza y hace backfill (ADR-0008); (c) los eventos y los logs no son
  raspables — son un flujo, no un estado instantáneo — y haría falta un segundo
  mecanismo de todas formas.
- **Modo embebido opcional además del separado.** Descartado: dos caminos de
  código para lo mismo es el doble de superficie de bugs y garantiza que uno de
  los dos esté siempre peor probado.

## Consecuencias

### Positivas
- Añadir un host es instalar un agente y darle un token. Cero cambios en el hub.
- Si el hub cae, los agentes siguen midiendo y bufferizando.
- El agente puede correr con muchos menos privilegios y menos superficie que el
  hub, que es el que abre puertos y sirve HTML.
- Permite en el futuro un hub externo (el VPS observando a iCloudServer) sin
  ningún cambio de arquitectura.

### Negativas
- Dos unidades systemd, dos ficheros de configuración y dos procesos donde
  podría haber uno. La instalación mono-host es algo más compleja de explicar.
- Serialización y deserialización de datos que en un monolito serían una llamada
  a función. Coste medido estimado: bajo, pero no nulo.
- **El hub es un punto único de fallo y vive en la máquina que se cuelga.** Se
  mitiga, no se elimina, con: buffer local en agentes (ADR-0008), deadman
  externo hacia el VPS (ADR-0009) y receptor de netconsole en UnRaid
  (ADR-0011).

## Notas de implementación

- El instalador mono-host despliega ambas unidades y configura el agente contra
  `127.0.0.1`. Debe ser un solo comando.
- El puerto de ingesta (8081 por defecto) **nunca** escucha en `0.0.0.0` por
  defecto: escucha en la interfaz de Tailscale o en loopback. Exponerlo es una
  acción explícita del administrador.
- El hub incluye su propio agente local (para observarse a sí mismo) como
  cualquier otro host. El hub no es especial en el modelo de datos.
