# Autenticación humana del hub con OIDC

Implementa [ADR-0019](../adr/0019-autenticacion-humana.md). Es **opcional**: si no configuras nada, el hub se comporta exactamente igual que antes.

## Por qué existe

Los tokens de ingesta de ADR-0008 autentican máquinas y el emparejamiento de ADR-0014 identifica dispositivos lectores. Ninguno responde a la pregunta de quién es la persona que abre la interfaz.

Poner un proxy de identidad delante —Cloudflare Access, por ejemplo— protege el borde, pero solo el borde. Si el origen escucha en una IP pública, conocerla basta para recibir la interfaz sin pasar por él. Esta autenticación cierra ese hueco desde dentro del hub.

> **Blindar el origen no sustituye a autenticar.** Sigue aplicando la [guía de blindaje del origen](blindaje-origen-cloudflare.md): son medidas complementarias, no alternativas.

## Lo que el hub NO hace

No guarda contraseñas, ni hashes, ni flujos de recuperación, ni MFA propio. La identidad vive en un proveedor que tú controlas, con su ciclo de vida de cuentas y su segundo factor. El hub solo comprueba que quien llama trae una sesión válida.

## Configuración

Cuatro variables de entorno. Con cualquiera de las tres primeras vacía, la autenticación queda desactivada:

| Variable | Obligatoria | Qué es |
| --- | --- | --- |
| `BITACORA_OIDC_ISSUER` | sí | URL del emisor, la que publica `/.well-known/openid-configuration` |
| `BITACORA_OIDC_CLIENT_ID` | sí | Identificador de cliente que te da el proveedor |
| `BITACORA_OIDC_REDIRECT_URL` | sí | `https://tu-hub/auth/callback` |
| `BITACORA_OIDC_CLIENT_SECRET` | según proveedor | Secreto de cliente |
| `BITACORA_OIDC_SESSION_TTL` | no | Duración de sesión, formato de Go (`12h` por defecto) |
| `BITACORA_OIDC_INSECURE_COOKIES` | no | Quita el flag `Secure` de las cookies. **Solo para desarrollo local sobre HTTP.** |

Son variables de entorno y no flags a propósito: un secreto pasado como flag es visible para cualquiera que pueda ejecutar `ps` en la máquina.

Con systemd, van en el `EnvironmentFile` de la unit del hub, con permisos `0640` y grupo del servicio.

## Ejemplo con Authentik

1. Crea un proveedor OAuth2/OpenID en Authentik.
2. Tipo de concesión: **Authorization Code**. El hub usa PKCE (S256) siempre.
3. URI de redirección: `https://tu-hub/auth/callback`, exacta.
4. Ámbitos: `openid`, `profile`, `email`.
5. Copia el *client id* y el *client secret* al `EnvironmentFile`.

```
BITACORA_OIDC_ISSUER=https://auth.tu-dominio/application/o/bitacora/
BITACORA_OIDC_CLIENT_ID=...
BITACORA_OIDC_CLIENT_SECRET=...
BITACORA_OIDC_REDIRECT_URL=https://tu-hub/auth/callback
```

## Qué cambia al activarlo

| Ruta | Sin OIDC | Con OIDC |
| --- | --- | --- |
| `/` (interfaz web) | abierta a quien alcance el origen | exige sesión; redirige al proveedor |
| `/v1/summary`, `/v1/events`, `/v1/logs`, `/v1/inventory` | token de dispositivo | token de dispositivo **o** sesión humana |
| `/v1/ingest` | token de máquina (ADR-0008) | **sin cambios**, token de máquina |
| `/v1/devices/pair`, `/v1/devices/claim` | arranque de emparejamiento | **sin cambios** |

**`/v1/ingest` no pasa por la autenticación humana, ni puede.** Está registrada como ruta exacta en el mux del hub, por delante del resto, y hay un test de regresión que falla si alguien lo cambia. Si esa frontera se moviera, todos los agentes dejarían de reportar a la vez y el hub seguiría pareciendo sano.

Una persona con sesión no necesita además un token de dispositivo: preguntarle dos veces por lo mismo no añade seguridad.

## Endpoints nuevos

- `GET /auth/login` — inicia el flujo. Admite `?return_to=`, restringido a rutas de este mismo hub.
- `GET /auth/callback` — retorno del proveedor.
- `GET /auth/logout` — cierra la sesión en el servidor, no solo en el navegador.
- `GET /auth/me` — devuelve la identidad activa en JSON.

## Límites conocidos

- **Las sesiones viven en memoria.** Reiniciar el hub obliga a volver a entrar. Es coherente con ADR-0003, que mantiene todas las capas embebidas y sin servicio externo obligatorio.
- **Si configuras un emisor y el hub no puede alcanzarlo al arrancar, no arranca.** Es deliberado: arrancar con la autenticación silenciosamente desactivada serviría la interfaz a cualquiera, que es justo el agujero que esto viene a cerrar.
- **No hay autorización todavía.** Cualquiera que el proveedor autentique entra. Restringir por grupo o por correo es una decisión aparte y necesita su propio ADR.
