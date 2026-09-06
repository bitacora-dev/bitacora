# ADR-0019: Autenticación humana del hub

- **Estado:** Propuesto
- **Fecha:** 2026-09-06

## Contexto

Los tokens de ingesta de ADR-0008 autentican máquinas y el emparejamiento por QR de ADR-0014 identifica dispositivos lectores. Ninguno resuelve la identidad de una persona que abre la interfaz web del hub.

Cloudflare Access protege el borde con Google, GitHub, OTP por correo y 2FA, pero solo se aplica en Cloudflare. Si el origen escucha en una IP pública, conocerla permite eludir Access y recibir la interfaz sin autenticación. Los endpoints de datos siguen exigiendo un bearer token, pero exponer la interfaz sigue siendo una frontera incorrecta.

Hacer de Cloudflare un requisito contradice el principio de no depender de terceros que ADR-0014 aplica a APNs. Una instalación autoalojada sin Cloudflare no puede quedar con un hub desprotegido. La restricción decisiva es la misma de ADR-0014: un único mantenedor, por afición y sin ingresos. La autenticación exige correcciones de seguridad, recuperación de cuentas, rotación de secretos y soporte permanente.

## Decisión

La recomendación es integrar **OIDC nativo opcional** en el hub y que el operador lo conecte a un proveedor que controle (por ejemplo, Authentik) o a Google o GitHub cuando acepte esa dependencia. El hub debe mantener una frontera de autenticación propia, pero no convertirse en un proveedor de identidad ni administrar contraseñas.

El despliegue debe blindar siempre el origen por separado. Para instalaciones con Cloudflare, [la guía de blindaje](../setup/blindaje-origen-cloudflare.md) describe firewall limitado a rangos oficiales o Cloudflare Tunnel. Sin Cloudflare, el operador debe usar su propio proxy de identidad, VPN y firewall restrictivo; ocultar el origen no sustituye la autenticación humana.

Este ADR no autoriza implementar autenticación. Su estado **Propuesto** reserva la decisión final al mantenedor.

## Alternativas consideradas

- **OIDC nativo contra un proveedor externo.** Recomendado: reutiliza MFA, recuperación y ciclo de vida de cuentas del proveedor, y permite que cada instalación elija uno. A cambio, incorpora OIDC al hub y el operador debe mantener un proveedor disponible; Google o GitHub añaden dependencia de terceros.
- **Usuario y contraseña propios con TOTP.** Descartado: evita depender de un IdP externo y funciona aislado, pero obliga a custodiar hashes, restablecimientos, MFA, sesiones y defensas ante ataques. Para un solo mantenedor es una forma fiable de publicar y luego sostener una vulnerabilidad.
- **Delegar toda la identidad a un proxy.** Válido como patrón de despliegue y útil para Cloudflare Access, Authelia o un proxy corporativo. Se descarta como única garantía: es fácil dejar el origen expuesto, no ofrece un camino uniforme sin terceros y traslada seguridad crítica a una configuración que el hub no puede verificar.

## Consecuencias

### Positivas

- Las instalaciones pueden conservar control de identidad y MFA mediante un IdP autoalojado.
- Se evita construir un almacén de credenciales y flujo de recuperación propios.
- Cloudflare permanece opcional y el blindaje de origen queda documentado.

### Negativas

- OIDC añade configuración, claves de cliente, redirecciones y compatibilidad que el único mantenedor tendrá que mantener.
- No usar Cloudflare no elimina el trabajo del operador: deberá proteger la red y disponer de un IdP o proxy bajo su control.
- Mientras este ADR siga Propuesto no existe autenticación humana nativa; los despliegues expuestos deben mantener proxy de identidad y origen blindado.

## Notas de implementación

- Una futura implementación debe separar tokens de agentes y dispositivos de sesiones humanas; no reutilizar credenciales de ingesta como login.
- No aceptar tráfico público directo al origen cuando se use un proxy de identidad: aplicar firewall por rangos de Cloudflare o Cloudflare Tunnel, o el equivalente operado por la instalación.
- Documentar una ruta autoalojada sin Cloudflare antes de aceptar el ADR.
