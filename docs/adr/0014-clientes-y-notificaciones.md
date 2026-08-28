# ADR-0014: Clientes nativos y notificaciones push

- **Estado:** Aceptado — cliente nativo **rechazado**, PWA como alternativa
- **Fecha:** 2026-08-28

## Contexto

El objetivo declarado incluye poder consultar el estado del servidor sin abrir un
SSH, lo que naturalmente incluye hacerlo desde el móvil. Existe además el deseo
de una aplicación nativa para iOS y macOS.

Hay un obstáculo estructural que conviene enunciar antes de diseñar nada: **las
notificaciones push de Apple (APNs) son incompatibles con un proyecto open source
autoalojado**. APNs exige una cuenta de desarrollador de pago y certificados
ligados a un identificador de aplicación concreto. Quien descargue el código y lo
compile no tendría notificaciones, salvo que exista un relay operado por alguien
con esos certificados — lo cual introduce un servicio central obligatorio, en
contradicción directa con el principio de no depender de terceros.

## Restricción determinante: un solo mantenedor

El proyecto lo mantiene una persona, como afición y sin ingresos. Esa es la
restricción real del proyecto, más que el dinero. Mantener un cliente Swift
implica un tercer lenguaje y un tercer ecosistema junto a Go y TypeScript,
renovaciones de firma, cambios de SDK de Apple cada año, y revisiones de la App
Store. Es una carga desproporcionada para el valor que aporta sobre una PWA.

Esta restricción convierte lo que era una decisión de prioridad en una decisión
de alcance.

## Decisión

### Separar consulta de notificación

Son dos problemas distintos y se resuelven por caminos distintos.

**Notificación → ntfy.** Self-hosted, con aplicaciones nativas para iOS y
Android ya publicadas y mantenidas, que resuelven el problema de APNs por su
cuenta y de forma legítima. Es el notificador por defecto (ADR-0009). Un usuario
recibe avisos en el móvil sin que este proyecto necesite tocar APNs.

**Consulta → interfaz web primero, cliente nativo después.**

### Fase 1-3: interfaz web adaptable

La interfaz web debe funcionar bien en un móvil desde el primer día. Sobre
Tailscale, abrir `http://icloudserver:8080` desde el iPhone ya cubre la mayor
parte del caso de uso, sin App Store, sin firma de código y sin mantener un
cliente aparte.

Requisitos móviles del web UI, no negociables:
- Diseño adaptable real, no una versión de escritorio encogida.
- Vista de resumen legible en una pantalla.
- Gráficas manejables con el dedo (uPlot lo soporta).
- Instalable como PWA, con icono en la pantalla de inicio.

### Cliente nativo Apple: rechazado

**No se construirá una aplicación nativa para iOS ni macOS.** Se rechaza de forma
explícita, no se aplaza: dejarlo como "fase 4" convertiría una decisión razonada
en una deuda pendiente que pesa sin producir nada.

Lo que se renuncia a tener, para que quede constancia:

- Widgets de pantalla de inicio con el estado del parque.
- Live Activities durante un backup en curso.
- Aplicación de barra de menús en macOS.

Es el 10 % del valor y una fracción muy superior del coste de mantenimiento.

Si en el futuro un contribuidor externo quiere construirla y mantenerla, será
bienvenida como repositorio separado dentro de la organización, con su propio
mantenedor. Nunca como responsabilidad del núcleo.

### Autenticación de clientes

Se mantiene el diseño de **token de dispositivo**, distinto del token de agente,
emitido desde la interfaz web y transferible por código QR. Sirve igual para la
PWA y para cualquier cliente de terceros que alguien quiera escribir.

## Alternativas consideradas

- **APNs propio con relay.** Descartado por el motivo del contexto: introduce un
  servicio central obligatorio operado por el mantenedor, contradiciendo el
  principio fundacional del proyecto. Podría ofrecerse como servicio opcional
  para quien lo quiera, pero nunca como requisito.
- **React Native o Flutter.** Descartado: pierde widgets, Live Activities y
  barra de menús, que es justamente lo que un navegador no puede dar. Si se
  renuncia a lo nativo, la PWA es mejor opción que un envoltorio.
- **SwiftUI multiplataforma en fase 4.** Era la propuesta original de este ADR y
  se rechaza por la restricción de mantenimiento descrita arriba.
- **Telegram como único canal.** Descartado como *único*: es un servicio de
  terceros. Se mantiene como notificador opcional, que es distinto.

## Consecuencias

### Positivas
- El proyecto no adquiere ninguna dependencia de la infraestructura de Apple ni
  de una cuenta de pago.
- ntfy resuelve el push móvil hoy, sin escribir una línea de código de cliente.
- El valor móvil se entrega en la fase 1, no en la 4.
- **Dos lenguajes en lugar de tres.** Es la consecuencia más importante para la
  viabilidad a largo plazo del proyecto.

### Negativas
- La experiencia móvil es la de un navegador: sin widgets y sin notificaciones
  enriquecidas más allá de lo que dé ntfy.
- En macOS se renuncia a la barra de menús, que era el mayor valor diferencial
  frente al navegador. Es la pérdida que más se notará.
- El proyecto será menos atractivo para quien valore una app nativa. Se asume.

## Notas de implementación

- Diseñar la API de lectura del hub **pensando en un cliente móvil desde el
  principio**: respuestas paginadas, endpoints de resumen que devuelvan en una
  sola llamada lo que una pantalla necesita, y SSE para actualización en vivo.
  Es barato hacerlo ahora e incómodo después.
- El endpoint `GET /v1/summary?host_id=...` debe devolver todo lo necesario para
  pintar la pantalla principal en una sola petición. Es el contrato del que
  dependerán tanto el widget como la barra de menús.
