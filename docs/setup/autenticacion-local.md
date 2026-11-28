# Autenticación local del hub (usuario, contraseña y TOTP)

Implementa [ADR-0023](../adr/0023-autenticacion-local-y-alcance-por-servidor.md) y [ADR-0025](../adr/0025-instalacion-y-recuperacion-local-de-credencial.md). Convive con [OIDC](autenticacion-humana-oidc.md): ninguna de las dos desactiva a la otra.

## Por qué existe

OIDC y Cloudflare Access protegen bien el borde, pero si el proveedor o el túnel se degradan te quedan sin panel justo durante una incidencia. La credencial local es el camino que no depende de terceros: vive en el servidor, la crea una persona que demuestra acceso administrativo local y el hub la verifica por sí mismo.

> **Blindar el origen no sustituye a autenticar.** Sigue aplicando la [guía de blindaje del origen](blindaje-origen-cloudflare.md).

## Lo que el hub NO hace

No hay registro de usuarios, ni recuperación por correo, ni enlaces de activación, ni credenciales por defecto, ni roles creados desde la interfaz. Existe **un solo sujeto**, `local:operator`. La web solo puede crear o sustituir su credencial **canjeando un token de un solo uso** que únicamente se entrega por la consola o el journal del hub, o por una orden local en el servidor. No existe ningún endpoint, botón ni API que emita, reenvíe o revele un token.

## Cambio incompatible: el hub ya no arranca abierto

Hasta ahora, un hub sin OIDC y sin credencial local servía el panel a cualquiera que alcanzara el origen. **Eso se acabó.** Ahora el hub falla cerrado:

| Ruta | Sin ninguna fuente de acceso |
| --- | --- |
| `/` y cualquier página | redirige a `/auth/setup` |
| `/v1/summary`, `/v1/events`, `/v1/logs`, `/v1/inventory`, `/v1/jobs/`, `/v1/hosts`, `/v1/enrollments`, `/v1/devices/*`, `/v1/actions/*` | `503 {"error":"setup_required"}`, **también con token de dispositivo** |
| `/v1/ingest` | **sin cambios**: los agentes siguen enviando con su token de máquina |
| `/v1/enroll` | sin cambios (canje de un código de alta de agente ya emitido) |
| `/v1/auth/session`, `/auth/setup`, `/auth/setup/*` | disponibles |

Los tokens de dispositivo se bloquean a propósito: el emparejamiento del primer dispositivo no exige credencial, así que dejarlo abierto le daría a un desconocido un token válido. Los dispositivos ya emparejados vuelven a funcionar en cuanto termina la instalación.

**No hay interruptor para volver al modo abierto.** ADR-0025 no lo contempla y ADR-0019 establece que una instalación sin Cloudflare no puede quedar con el hub desprotegido; un proxy de identidad delante no sustituye a esta frontera.

### Ruta de actualización (una sola vez)

Para una instalación que hoy funciona sin autenticación, elige **una** de estas opciones antes o justo después de actualizar:

1. **Instalación web** (recomendada). Actualiza y arranca el hub. En su log aparece un bloque con el token de instalación; abre `https://tu-hub/auth/setup` y sigue los pasos de la [instalación inicial](#instalación-inicial-desde-la-web).
2. **CLI, como hasta ahora.** `bitacora-hub auth local init` sigue funcionando igual (contraseña por TTY). Si la ejecutas antes de actualizar, el hub arranca ya con la credencial y nunca entra en modo instalación.
3. **OIDC.** Configura las variables de [OIDC](autenticacion-humana-oidc.md); con un proveedor configurado el hub no entra en modo instalación.

Mientras no completes una de las tres, los agentes siguen enviando datos (no se pierde nada), pero nadie puede leerlos.

## Configuración

| Variable / flag | Obligatoria | Qué es |
| --- | --- | --- |
| `BITACORA_LOCAL_AUTH` | no | Booleano. Se mantiene por compatibilidad: con `true` y sin credencial, `/v1/auth/session` informa `local_pending_initialization: true`. |
| `BITACORA_LOCAL_AUTH_PATH` | no | Ruta de la credencial. Por defecto `/etc/bitacora/local-auth.json`. |
| `BITACORA_LOCAL_AUTH_KEY_PATH` | no | Ruta de la clave AES-256-GCM que cifra el secreto TOTP. Por defecto `/etc/bitacora/local-auth.key`. |
| `-setup-token-ttl` | no | Vida del token de instalación que imprime el hub. Por defecto `30m`, máximo `24h`; un valor fuera de rango impide arrancar. |

El estado del token de instalación o recuperación vive en `local-auth-setup.json`, **en el mismo directorio que la credencial**. Solo guarda el hash SHA-256 del token, su caducidad y si se consumió; nunca el token en claro. Todos estos ficheros tienen modo `0600`.

Si usas rutas personalizadas, **exporta las mismas variables también para la CLI**: la CLI y el servidor resuelven las rutas desde la misma configuración.

## Los estados posibles

| Estado | Qué hace el hub |
| --- | --- |
| Sin OIDC y sin credencial | **Modo instalación.** Imprime un token de instalación y solo sirve `/auth/setup`. |
| Sin OIDC y con la credencial desactivada (`auth local disable`) | También falla cerrado, pero **no** imprime token: un token de instalación nunca sobrescribe una credencial. Usa `bitacora-hub auth local recover`. |
| Credencial local operativa | Login con usuario, contraseña y TOTP (o un código de recuperación). |
| OIDC configurado (con o sin credencial local) | Login por el proveedor; la credencial local, si existe, sigue siendo un camino alternativo. |

## Instalación inicial desde la web

1. Arranca el hub. En su salida de error (el journal con systemd, `docker logs` en un contenedor) aparece **una sola vez**:

   ```
   bitacora-hub: ================================================================
   bitacora-hub: no login source is configured; the dashboard and data API are closed until setup completes (ADR-0025).
   bitacora-hub: open /auth/setup on this hub and enter this one-time install token (valid until 2026-11-21T10:30:00Z):
   bitacora-hub:     <token>
   bitacora-hub: it is shown only here. If it expires, restart the hub or run `bitacora-hub auth local recover`.
   bitacora-hub: ================================================================
   ```

   El log pasa a ser material sensible mientras el token esté vigente: protege sus permisos y su retención.

2. Abre `https://tu-hub/auth/setup` e introduce el token, un **usuario** (libre, no es un correo) y una **contraseña** de al menos 12 caracteres. La interfaz orienta sobre su fortaleza; el hub valida la longitud y la guarda con Argon2id (los parámetros de ADR-0023). En este paso no se escribe nada en disco y el token todavía no se consume.
3. Da de alta el TOTP escaneando el QR o tecleando el secreto (SHA-1, 6 dígitos, 30 s) y confirma con el código que muestra la aplicación. Cinco códigos erróneos descartan el alta en curso; puedes empezar de nuevo con el mismo token.
4. Al confirmar, el hub **consume el token**, escribe la credencial y muestra **una sola vez** los diez códigos de recuperación. Ya tienes sesión: el panel se abre y `/auth/setup` deja de ofrecer nada.

Si el token caduca antes de terminar, **reinicia el hub** (cada arranque sin credencial emite uno nuevo e invalida el anterior) o ejecuta `bitacora-hub auth local recover`.

El usuario es solo el atributo de acceso y presentación: el sujeto de auditoría sigue siendo siempre `local:operator`, y los logs no registran ni el usuario ni ningún secreto. Una credencial creada con `auth local init` no tiene usuario y se accede dejando el campo vacío.

## Inicializar o rotar por CLI

`bitacora-hub auth local init` sigue disponible y es equivalente a la instalación web, pero sin usuario:

1. Pide la contraseña nueva dos veces, siempre por TTY. **Nunca** por argumento ni por variable de entorno.
2. Imprime **una sola vez** el secreto TOTP en base32 y diez códigos de recuperación.
3. Escribe `local-auth.json` y `local-auth.key` con modo `0600`. En systemd quedan en propiedad del usuario `bitacora`; en el contenedor oficial, que no tiene esa cuenta, quedan en propiedad de root con `0600`.

El resto del ciclo de vida:

```sh
bitacora-hub auth local rotate-password            # pide la contraseña actual
bitacora-hub auth local rotate-totp                # nuevo secreto + nuevos diez códigos
bitacora-hub auth local regenerate-recovery-codes  # solo los diez códigos
bitacora-hub auth local disable                    # desactiva la fuente local
bitacora-hub auth local recover                    # token de recuperación por pérdida total
```

Las cuatro primeras incrementan la generación de sesión y **revocan todas las sesiones locales vivas**.

## Acceso, bloqueo y recuperación

- La pantalla de acceso está en `/auth/login`, servida desde el binario por `go:embed`: no depende de red ni de CDN.
- El segundo factor es el código TOTP o, en su lugar, **uno de los diez códigos de recuperación**, que vale para un único inicio de sesión.
- **Cinco intentos fallidos consecutivos bloquean al operador durante 15 minutos.** El bloqueo se persiste: reiniciar el hub no lo esquiva. Un usuario incorrecto cuenta como un intento fallido más, indistinguible de una contraseña incorrecta.
- Una sesión humana dura **12 horas absolutas** y se invalida al cerrar sesión, al vencer, al rotar la credencial o al **reiniciar el hub**.
- Perder el TOTP teniendo la contraseña **no** es una pérdida total: usa un código de recuperación y luego `rotate-totp`.

### Recuperación por pérdida total

Si se pierden a la vez usuario o contraseña, TOTP y los códigos, en el servidor (dentro del contenedor, si aplica):

```sh
bitacora-hub auth local recover
```

La orden no acepta argumentos ni secretos y no revela el usuario. Necesita poder escribir en el directorio de la credencial, que es justamente el acceso administrativo local que exige ADR-0025: la frontera son los permisos de esos ficheros, no una comprobación de que la orden corre dentro del proceso o el contenedor del hub. Imprime **una vez** un token de recuperación válido 30 minutos e invalida cualquier token anterior. El hub en marcha lo detecta sin reiniciarse: abre `https://tu-hub/auth/setup` y repite el flujo web. En un hub ya configurado, `/v1/auth/session` **no** anuncia que hay una recuperación pendiente (`setup_available` sigue en `false`); la pantalla de instalación ofrece el formulario igualmente y el hub valida el token. Al canjearlo se sustituyen usuario, contraseña, TOTP y los diez códigos, se reactiva una credencial desactivada, se incrementa la generación y **se cierran todas las sesiones locales**.

Perder también el acceso administrativo al servidor elimina toda vía de recuperación: solo queda reconstruir o redesplegar.

## Contenedores: volumen persistente obligatorio

La credencial (`local-auth.json`), la clave que cifra el TOTP (`local-auth.key`) y el estado del token (`local-auth-setup.json`) **tienen que vivir en un volumen persistente**. La imagen declara `VOLUME /etc/bitacora`, pero un volumen anónimo se pierde al recrear el contenedor: monta uno con nombre o un bind mount explícito.

Ese mismo directorio contiene también, por defecto, la configuración del hub: `rules/` (reglas de extracción), `alert-rules/` (reglas de alerta) y `notifications.yaml`. El volumen las cubre a la vez.

**No montes `/etc/bitacora` en solo lectura** (o, si lo haces para proteger la configuración, mueve la credencial a otra ruta escribible con `BITACORA_LOCAL_AUTH_PATH` y `BITACORA_LOCAL_AUTH_KEY_PATH`). Con el directorio de la credencial en solo lectura el hub no puede emitir el token al arrancar ni completar la instalación. Si ocurre durante el flujo web, la instalación se detiene con un aviso claro **antes** de consumir el token, de modo que basta con corregir el montaje y volver a intentarlo con el mismo token.

```sh
docker run -d --name bitacora-hub \
  -v bitacora-data:/var/lib/bitacora \
  -v bitacora-etc:/etc/bitacora \
  -p 8081:8081 ghcr.io/bitacora-dev/bitacora-hub:<versión>
```

Si prefieres un único volumen, apunta `BITACORA_LOCAL_AUTH_PATH` y `BITACORA_LOCAL_AUTH_KEY_PATH` a rutas dentro de `/var/lib/bitacora` (por ejemplo `/var/lib/bitacora/auth/local-auth.json`). Arrancar sin volumen no es un modo degradado: una recreación borra la credencial, el hub vuelve a modo instalación y emite un token nuevo en el log.

Para la recuperación en un contenedor: `docker exec <contenedor> bitacora-hub auth local recover`.

## Sesiones en memoria

Las sesiones se guardan en memoria (ADR-0003). **Reiniciar o redesplegar el hub cierra la sesión de todo el mundo**; hay que volver a entrar con usuario, contraseña y TOTP. La credencial y el bloqueo, en cambio, sí persisten.

## Comprobar en qué estado está un hub

```sh
curl -s https://tu-hub/v1/auth/session
```

```json
{
  "auth_enabled": true,
  "authenticated": false,
  "local_enabled": true,
  "oidc_enabled": false,
  "local_pending_initialization": false,
  "setup_required": false,
  "setup_available": false
}
```

Responde `200` siempre, incluso sin sesión, y no revela nada de la credencial ni del token.

| Respuesta | Significado |
| --- | --- |
| `setup_required: true` | No hay ninguna fuente de acceso: el hub solo sirve `/auth/setup`. |
| `setup_available: true` | Hay un token pendiente de canjear. Solo se informa cuando `setup_required` es `true`. |
| `local_pending_initialization: true` | `BITACORA_LOCAL_AUTH=true` pero aún no hay credencial. |
| `local_enabled: true` | Credencial lista; la pantalla muestra usuario, contraseña y TOTP. |
| `authenticated: true` | Esta petición trae una sesión válida; incluye `identity`. |

## Pasos exactos en Dokploy para `bitacora-dev`

Estos pasos **los tiene que hacer la persona propietaria del servidor**: ningún agente toca Dokploy ni el servidor.

El despliegue actual monta `/var/lib/dokploy-bitacora/data-dev → /var/lib/bitacora` y `/var/lib/dokploy-bitacora/etc-dev → /etc/bitacora`. El segundo bind mount ya cubre la ruta por defecto de la credencial y del token.

1. Confirma que el bind mount de `/etc/bitacora` sigue declarado en **Mounts**.
2. Si el hub ya tiene credencial local, no hay nada que hacer: la actualización no cambia nada visible.
3. Si no la tiene, redespliega y busca en el log del servicio el bloque `install token`. Abre `https://bitacora-dev.acaso.es/auth/setup` y completa los pasos. Si el token caducó, redespliega o ejecuta `docker exec <contenedor-del-servicio> bitacora-hub auth local recover`.
4. Verifica desde fuera que `curl -s https://bitacora-dev.acaso.es/v1/auth/session` responde `setup_required: false` y `local_enabled: true`.
5. Deja el blindaje de Cloudflare como está. Esta credencial es el camino que te queda **cuando** Cloudflare no responde; no es un motivo para quitarlo.

> La app de producción `bitacora.acaso.es` está parada a propósito. No la toques en este proceso.

## Fuera del alcance de este documento

- **Filtrado por host de las rutas de lectura**: ver ADR-0023.
- **Varios operadores, alta de usuarios o roles desde la interfaz**: prohibido por ADR-0023; necesitaría otro ADR.
- **Cambios en OIDC**: ver [su propia guía](autenticacion-humana-oidc.md).
