# ADR-0029: Modo en vivo bajo demanda para CPU y red

- **Estado:** Propuesto
- **Fecha:** 2026-11-27

## Contexto

ADR-0007 fija diez segundos como cadencia de alta frecuencia. La interfaz usa
la misma cadencia para `/v1/summary`, y `web/DESIGN.md` prohíbe aumentarla solo
para que el panel parezca animado: debe interpolar entre muestras reales. El
trabajo #2077 ya cubre esa alternativa de coste cero.

Hay, sin embargo, una necesidad distinta: mientras una persona observa un
host, dos segundos entre muestras reales de CPU y tráfico de red reducen la
latencia perceptible de una variación. Hacerlo permanentemente multiplicaría
por cinco el trabajo, el tráfico y las muestras de todos los hosts, incluso sin
paneles abiertos.

ADR-0024 obliga a tratar el presupuesto como evidencia y no como intuición. La
medición siguiente se ejecutó el 2026-11-27 en Linux 6.8.0-151-generic,
x86_64, Go 1.26.5, con el runtime real de ADR-0007 y los collectors reales
`cpu` y `network`, un `Sink` de descarte, `/proc` del host, dos ventanas de
120 s y RSS/CPU leídos de `/proc/<pid>/status` y `/proc/<pid>/stat`. CPU se
calculó como `(cpu_final - cpu_inicial) / tiempo_mural`; RSS pico se sondeó cada
segundo.

| Cadencia | Ciclos esperados por collector | CPU de proceso | RSS final / pico |
| --- | ---: | ---: | ---: |
| 10 s | 12 | 0,025 % de un core (0,030 s) | 10,04 / 10,04 MiB |
| 2 s | 60 | 0,092 % de un core (0,110 s) | 11,57 / 11,57 MiB |

La prueba mide el coste incremental real de programar y ejecutar esos dos
collectors, no el RSS del agente completo ni transporte, buffer o TSDB. Por
tanto no contradice la evidencia previa de ADR-0024 (72,6 MiB estabilizados y
84,1 MiB de pico para el agente completo, por encima del presupuesto de
60 MiB); tampoco autoriza a atribuir el RSS completo a collectors individuales.
Sí demuestra que la mayor frecuencia de CPU/red aumenta CPU en esta ventana
0,067 puntos porcentuales y el pico observado 1,53 MiB en ese proceso acotado.

ADR-0026 ya define un canal hub→agente dentro de `IngestResponse`, con
vocabulario cerrado, caducidad y sin abrir conexiones hacia el agente. Es la
única base admisible para una petición temporal de muestreo. El estado de que
un panel está siendo visto procede de una sesión humana autenticada y de su
suscripción explícita, no de una métrica, alerta o inventario observado.

## Decisión

Se diseña un perfil cerrado `LIVE_CPU_NETWORK` para el canal de órdenes de
ADR-0026. Mientras el hub tenga al menos una suscripción visible, autenticada y
autorizada para un host, solicita al agente una cadencia de **2 s** solo para
`cpu` y `network`. Los demás collectors conservan sus intervalos de ADR-0007.
El perfil **no incluye E/S de disco**: antes de añadirla se requiere una
medición aislada equivalente y una revisión de cardinalidad, latencia de
`/proc/diskstats` y coste de la serie.

La orden contiene únicamente el enum de perfil, un `request_id` generado por
el hub y `expires_at_ms`; no transporta intervalos, nombres de métricas ni
parámetros libres. El lease dura 45 s y el hub lo renueva mientras siga
existiendo al menos una suscripción válida. El agente revierte localmente a
10 s al vencer el lease, incluso si el hub o el navegador desaparecen. Las
renovaciones se coalescen y no deben crear goroutines ni tickers adicionales
por panel: existe un estado de perfil por host y un scheduler por collector.

Esta orden sigue siendo una **lectura** bajo ADR-0012: no ejecuta comandos, no
usa helpers privilegiados, no escribe en la máquina observada ni deriva una
acción de los datos observados. Cambia temporalmente la frecuencia de lectores
ya permitidos debido a una intención explícita de una persona. Una alerta, un
umbral, una métrica o cualquier otro dato observado no puede crear ni renovar
el lease.

Las muestras rápidas se transportan como clase `live` y el hub las mantiene en
una caché efímera por host, con TTL igual al lease. No se insertan en el TSDB ni
alteran retención, compactación o consultas históricas; la última muestra
normal y la serie de diez segundos siguen siendo el historial canónico. Esto
evita multiplicar por cinco la ocupación de esas series solo por una pantalla
abierta y evita presentar la vista viva como histórico de precisión distinta.

La interfaz hace sondeo de resumen cada 2 s solo para el host y las tarjetas
en modo en vivo. Al recibir `document.hidden` detiene ese sondeo y libera su
suscripción; al volver visible la adquiere de nuevo. El hub expira las
suscripciones aunque no llegue esa liberación. No se usa SSE en esta fase:
con uno o pocos paneles visibles, el sondeo autenticado aprovecha los endpoint
actuales, evita mantener conexiones en móviles y deja el coste acotado por el
lease. La interfaz conserva las transiciones entre muestras reales y respeta
`prefers-reduced-motion`.

Esta decisión no habilita implementación mientras ADR-0026 sea `Propuesto`.
La implementación queda condicionada además a una medición de agente completo
con transporte y buffer que demuestre que el modo activo no empeora el exceso
ya documentado por ADR-0024 o a una decisión posterior que trate ese exceso.

## Alternativas consideradas

- **Quedarse en 10 s e interpolar entre muestras reales.** Es el coste cero y
  la opción ya iniciada en #2077. Se conserva como comportamiento normal y
  fallback; no satisface una inspección que necesite una muestra real antes de
  diez segundos.
- **Cadencia de 2 s permanente para todos los hosts.** Rechazada: multiplica
  por cinco el trabajo y las series incluso cuando nadie consulta el panel.
- **Incluir E/S de disco en el perfil inicial.** Rechazada: no hay medición de
  ese collector a 2 s y sus series/dispositivos elevan tanto la cardinalidad
  como el coste de almacenamiento.
- **Guardar las muestras rápidas en el TSDB.** Rechazada: aumenta por cinco
  ingestión, bloques y retención de CPU/red para un beneficio que es visual y
  temporal; además mezcla detalle efímero con el historial canónico.
- **SSE o WebSocket hacia el navegador.** Rechazada para este alcance: reduce
  peticiones pero añade conexiones vivas, reconexión, gestión de suspensión de
  móvil y backpressure. Puede reevaluarse con medición de paneles concurrentes.
- **Abrir un canal hub→agente.** Rechazada por ADR-0026: rompe la iniciativa de
  red del agente, NAT y la frontera de superficie de ataque.

## Consecuencias

### Positivas

- Un panel observado obtiene muestras reales de CPU y red con hasta 2 s de
  antigüedad sin convertir esa carga en permanente.
- El lease limita el daño de pestañas abandonadas y funciona aunque el evento
  del navegador no llegue.
- La medición explícita evita presentar el modo como gratuito y separa su
  coste incremental del RSS global de ADR-0024.
- El historial y la retención actuales no cambian por una decisión de interfaz.

### Negativas

- Añade estado temporal y auditoría de suscripciones, leases y expiraciones en
  el hub, además de reprogramación segura en el agente.
- El sondeo a 2 s consume batería y datos mientras el panel está visible; la
  pausa por visibilidad lo reduce, no lo elimina.
- La caché efímera puede perderse al reiniciar el hub y no permite reconstruir
  una investigación histórica a dos segundos.
- El agente completo ya incumple el límite de RSS observado en ADR-0024; el
  modo no debe usarse como argumento para normalizar ese exceso.
- El canal de órdenes gana otro enum y otro estado temporal, por lo que deberá
  seguir siendo cerrado y revisado con el mismo rigor que ADR-0026.

## Tareas de implementación derivadas

1. Aceptar o rechazar ADR-0026 y este ADR antes de modificar protocolo o
   scheduler.
2. Medir el binario de agente completo con transporte y buffer a 10 s y 2 s,
   con solo CPU/red acelerados; publicar CPU, RSS estable y pico reproducibles.
3. Extender el protobuf de ADR-0026 con un enum cerrado de perfil vivo, lease y
   expiración; probar que entradas desconocidas, caducadas y duplicadas no
   cambian la cadencia.
4. Añadir al runtime una reconfiguración por perfil, coalescida y libre de
   fugas, con restauración automática a 10 s y pruebas de carrera.
5. Implementar en el hub suscripciones autenticadas por host, renovación de
   45 s, expiración, auditoría y caché efímera sin escritura de muestras
   `live` en TSDB.
6. Implementar en la interfaz el modo visible, pausa en `document.hidden`,
   sondeo de 2 s limitado al host activo y las pruebas de accesibilidad y
   consumo correspondientes.
7. Antes de incluir E/S de disco, proponer un ADR o una enmienda con medición
   aislada, cardinalidad y decisión de almacenamiento.
