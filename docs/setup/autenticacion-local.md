# Autenticación local del hub (usuario, contraseña y TOTP)

Implementa [ADR-0023](../adr/0023-autenticacion-local-y-alcance-por-servidor.md). Es **opcional** y convive con [OIDC](autenticacion-humana-oidc.md): ninguna de las dos desactiva a la otra.

## Por qué existe

OIDC y Cloudflare Access protegen bien el borde, pero si el proveedor o el túnel se degradan te quedan sin panel justo durante una incidencia. La credencial local es el camino que no depende de terceros: vive en el servidor, la crea una persona con acceso administrativo local y el hub la verifica por sí mismo.

> **Blindar el origen no sustituye a autenticar.** Sigue aplicando la [guía de blindaje del origen](blindaje-origen-cloudflare.md).

## Lo que el hub NO hace

No hay registro, ni alta desde la web, ni recuperación por correo, ni restablecimiento remoto, ni credenciales por defecto. Existe **un solo sujeto**, `local:operator`, y todo su ciclo de vida pasa por la CLI del servidor con la contraseña pedida por TTY. Si pierdes contraseña, TOTP y los diez códigos de recuperación a la vez, la única salida es acceso administrativo local al servidor para rotar la credencial.

## Configuración

| Variable | Obligatoria | Qué es |
| --- | --- | --- |
| `BITACORA_LOCAL_AUTH` | no | Booleano. Activa la fuente local **antes** de que exista la credencial. |
| `BITACORA_LOCAL_AUTH_PATH` | no | Ruta de la credencial. Por defecto `/etc/bitacora/local-auth.json`. |
| `BITACORA_LOCAL_AUTH_KEY_PATH` | no | Ruta de la clave AES-256-GCM que cifra el secreto TOTP. Por defecto `/etc/bitacora/local-auth.key`. |

Tres reglas que conviene tener claras:

1. **Una credencial ya inicializada activa la fuente local aunque no pongas `BITACORA_LOCAL_AUTH`.** Las instalaciones anteriores a esta variable siguen funcionando sin tocar nada.
2. **`BITACORA_LOCAL_AUTH=false` no desactiva una credencial ya inicializada.** Es deliberado: si una variable de entorno pudiera revocar la credencial, una errata en un despliegue dejaría el panel sin ninguna frontera humana y sin que nada falle de forma visible. Para desactivarla de verdad está `bitacora-hub auth local disable`, que persiste la decisión y revoca las sesiones vivas.
3. **Un valor que no sea booleano es un error de arranque.** `BITACORA_LOCAL_AUTH=on` para el hub en lugar de leerse en silencio como «desactivado»; un despliegue que *parece* autenticado y no lo está es exactamente el fallo que esta variable evita.

Las rutas son configurables porque [ADR-0025](../adr/0025-instalacion-y-recuperacion-local-de-credencial.md) exige que, en contenedor, **la credencial y la clave vivan en un volumen persistente**. Si no lo montas, una recreación del contenedor borra la credencial y te quedas fuera: eso es una configuración incorrecta, no un modo degradado.

## Los tres estados posibles

| Estado | `BITACORA_LOCAL_AUTH` | Ficheros | Qué hace el hub |
| --- | --- | --- | --- |
| Inerte | sin poner o `false` | no existen | Sin frontera humana: el panel se sirve a quien alcance el origen (el comportamiento anterior a ADR-0019). |
| Activada, pendiente de inicializar | `true` | no existen | **La frontera YA está puesta.** `/` redirige a `/auth/login`, las rutas de datos responden 401 y la pantalla de acceso explica que falta ejecutar la CLI. No hay ninguna petición que pueda crear la cuenta del operador. |
| Activada y operativa | cualquiera | existen y son válidos | Login con contraseña y TOTP (o un código de recuperación). |

El estado intermedio es la parte que importa en seguridad: **nadie puede reclamar el hub por ser el primero en llegar.** La creación de la credencial solo ocurre en `bitacora-hub auth local init`, que exige un TTY del servidor; no existe pantalla, endpoint ni API que la cree. El alta web tokenizada que describe ADR-0025 está **propuesta, no implementada**.

> La frontera se pone antes de que exista la credencial a propósito. Si el hub esperase a tener credencial para empezar a pedir sesión, el hueco entre «activo» y «inicializado» sería precisamente una ventana con el panel abierto.

`/v1/ingest` **no pasa nunca** por esta frontera, tampoco en el estado pendiente: si lo hiciera, todos los agentes se callarían a la vez justo mientras intentas diagnosticar el hub.

## Inicializar el primer operador y dar de alta el TOTP

En el servidor, con un TTY real y como el usuario que luego leerá los ficheros:

```sh
bitacora-hub auth local init
```

1. Pide la contraseña nueva dos veces, siempre por TTY. **Nunca** por argumento ni por variable de entorno: un secreto en `argv` lo ve cualquiera que pueda ejecutar `ps`.
2. Imprime **una sola vez** el secreto TOTP en base32. Dalo de alta en la aplicación autenticadora (SHA-1, 6 dígitos, periodo de 30 s, RFC 6238).
3. Imprime **una sola vez** diez códigos de recuperación. Guárdalos fuera del servidor.
4. Escribe `local-auth.json` y `local-auth.key` con modo `0600`. En una instalación con systemd quedan en propiedad del usuario `bitacora`; en el contenedor oficial, que no tiene esa cuenta, quedan en propiedad de root con `0600`.

Si usas rutas personalizadas, **exporta las mismas variables también para la CLI**: la CLI y el servidor resuelven la ruta desde la misma configuración, y si solo la pones en el servidor inicializarás una credencial que el hub nunca va a leer.

El resto del ciclo de vida:

```sh
bitacora-hub auth local rotate-password            # pide la contraseña actual
bitacora-hub auth local rotate-totp                # nuevo secreto + nuevos diez códigos
bitacora-hub auth local regenerate-recovery-codes  # solo los diez códigos
bitacora-hub auth local disable                    # desactiva la fuente local
```

Cualquiera de los cuatro incrementa la generación de sesión y **revoca todas las sesiones locales vivas**.

## Acceso, bloqueo y recuperación

- La pantalla de acceso está en `/auth/login`, servida desde el binario por `go:embed`: no depende de red ni de CDN, que es justo lo que hace falta cuando el panel es lo único que te queda.
- El segundo factor es el código TOTP o, en su lugar, **uno de los diez códigos de recuperación**. Un código de recuperación vale para un único inicio de sesión y se borra al aceptarse.
- **Cinco intentos fallidos consecutivos bloquean al operador durante 15 minutos.** El bloqueo se persiste: reiniciar el hub no lo esquiva, y durante el bloqueo se rechaza incluso la credencial correcta. La respuesta es `429` con el instante de desbloqueo, y la interfaz lo muestra con la hora.
- Una sesión humana dura **12 horas absolutas**, no se renueva por actividad y se invalida al cerrar sesión, al vencer, al reiniciar el hub o al rotar la credencial.
- Perder el TOTP teniendo la contraseña **no** es una pérdida total: usa un código de recuperación y luego `rotate-totp`. La pérdida total (contraseña + TOTP + los diez códigos) exige acceso administrativo local al servidor; ADR-0025 describe un canje tokenizado para ese caso, pero todavía no está implementado.

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
  "local_pending_initialization": false
}
```

Responde `200` siempre, incluso sin sesión, y no revela nada de la credencial: solo qué rutas de acceso ofrece el hub. Es así a propósito, porque un `404` o un `401` ahí es indistinguible de «este binario no tiene autenticación» para quien está diagnosticando un despliegue.

Cómo leerlo:

| Respuesta | Significado |
| --- | --- |
| `auth_enabled: false` | No hay ninguna frontera humana configurada. |
| `local_pending_initialization: true` | Activada pero sin credencial: falta `auth local init` en el servidor. |
| `local_enabled: true` | Credencial lista; la pantalla muestra contraseña y TOTP. |
| `authenticated: true` | Esta petición trae una sesión válida; incluye `identity`. |

`GET /auth/me` sigue existiendo y devuelve la identidad activa (`401` sin sesión).

## Pasos exactos en Dokploy para `bitacora-dev`

Estos pasos **los tiene que hacer la persona propietaria del servidor**: ningún agente toca Dokploy ni el servidor.

El despliegue actual monta `/var/lib/dokploy-bitacora/data-dev → /var/lib/bitacora` y `/var/lib/dokploy-bitacora/etc-dev → /etc/bitacora`. El segundo bind mount ya cubre la ruta por defecto de la credencial, así que **no hace falta cambiar las rutas**: basta con activar la fuente local.

1. En la app de Dokploy de `bitacora-dev`, en **Environment**, añade:

   ```
   BITACORA_LOCAL_AUTH=true
   ```

   No añadas `BITACORA_LOCAL_AUTH_PATH` ni `BITACORA_LOCAL_AUTH_KEY_PATH` salvo que quieras mover la credencial; si las pones, apunta a una ruta **dentro de un bind mount persistente** (por ejemplo `/var/lib/bitacora/local-auth.json`), nunca al sistema de ficheros efímero del contenedor.

2. Confirma que el bind mount de `/etc/bitacora` sigue declarado en **Mounts**. Si la credencial no está en un volumen persistente, el siguiente redespliegue la borra.

3. Redespliega. A partir de este momento **el panel queda cerrado**: `https://bitacora-dev.acaso.es/` redirige a `/auth/login`, que mostrará el aviso de «activada pero sin inicializar». Eso es lo esperado, no un fallo. En el log del servicio aparece la línea:

   ```
   bitacora-hub: local authentication is enabled but not initialized; run `bitacora-hub auth local init` on this server (credential: /etc/bitacora/local-auth.json, key: /etc/bitacora/local-auth.key)
   ```

4. Abre una shell interactiva en el contenedor —**interactiva de verdad, con TTY**, porque la contraseña no se acepta de otra forma— y ejecuta la inicialización:

   ```sh
   docker exec -it <contenedor-del-servicio> bitacora-hub auth local init
   ```

   Si el servicio corre en Swarm, el nombre del contenedor es el de la tarea (por ejemplo `bitcora-bitcora-development-<sufijo>.1.<id>`); `docker ps` lo lista. La consola de Dokploy también sirve siempre que dé un TTY: sin TTY el comando se niega a continuar en lugar de leer la contraseña de otro sitio.

5. Apunta el secreto TOTP en la aplicación autenticadora y guarda los diez códigos de recuperación fuera del servidor. **No se vuelven a mostrar.**

6. Verifica desde fuera:

   ```sh
   curl -s https://bitacora-dev.acaso.es/v1/auth/session
   ```

   Debe responder `local_enabled: true` y `local_pending_initialization: false`.

7. Entra en `https://bitacora-dev.acaso.es/auth/login` con la contraseña y el código de seis dígitos. Al entrar, el panel se abre con la sesión del operador.

8. Deja el blindaje de Cloudflare como está. Esta credencial es el camino que te queda **cuando** Cloudflare no responde; no es un motivo para quitarla.

> La app de producción `bitacora.acaso.es` está parada a propósito. No la toques en este proceso.

## Fuera del alcance de este documento

- **Filtrado por host de las rutas de lectura.** ADR-0023 modela `view`/`operate` por host, y `hubauth.ScopeFor` ya devuelve ambas capacidades sobre todos los hosts para `local:operator`, pero las rutas de lectura **todavía no filtran**. Hoy hay un único operador, así que no cambia nada observable; con varios operadores sí haría falta.
- **Alta web tokenizada y recuperación remota por token** (ADR-0025): propuesto, no implementado.
- **Cambios en OIDC**: ver [su propia guía](autenticacion-humana-oidc.md).
