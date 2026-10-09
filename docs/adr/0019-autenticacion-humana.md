# ADR-0019: Autenticación humana del hub

- **Estado:** Sustituido parcialmente por ADR-0023
- **Fecha:** 2026-09-06

## Contexto

Los tokens de ingesta de ADR-0008 autentican máquinas y el emparejamiento por QR de ADR-0014 identifica dispositivos lectores. Ninguno resuelve la identidad de una persona que abre la interfaz web del hub.

Cloudflare Access protege el borde con Google, GitHub, OTP por correo y 2FA, pero solo se aplica en Cloudflare. Si el origen escucha en una IP pública, conocerla permite eludir Access y recibir la interfaz sin autenticación. Los endpoints de datos siguen exigiendo un bearer token, pero exponer la interfaz sigue siendo una frontera incorrecta.

Hacer de Cloudflare un requisito contradice el principio de no depender de terceros que ADR-0014 aplica a APNs. Una instalación autoalojada sin Cloudflare no puede quedar con un hub desprotegido. La restricción decisiva es la misma de ADR-0014: un único mantenedor, por afición y sin ingresos. La autenticación exige correcciones de seguridad, recuperación de cuentas, rotación de secretos y soporte permanente.

## Decisión

Se integra **OIDC nativo opcional** en el hub y el operador puede conectarlo a un proveedor que controle (por ejemplo, Authentik) o a Google o GitHub cuando acepte esa dependencia. El hub mantiene una frontera de autenticación propia, pero no administra el ciclo de vida de las cuentas OIDC.

El despliegue debe blindar siempre el origen por separado. Para instalaciones con Cloudflare, [la guía de blindaje](../setup/blindaje-origen-cloudflare.md) describe firewall limitado a rangos oficiales o Cloudflare Tunnel. Sin Cloudflare, el operador debe usar su propio proxy de identidad, VPN y firewall restrictivo; ocultar el origen no sustituye la autenticación humana.

Este ADR autorizó e introdujo OIDC nativo opcional. La decisión de descartar una
credencial local propia queda sustituida por [ADR-0023](0023-autenticacion-local-y-alcance-por-servidor.md): conserva OIDC como fuente de identidad y añade una fuente local deliberadamente limitada para conservar disponibilidad durante una caída del IdP o de Cloudflare.

## Alternativas consideradas

- **OIDC nativo contra un proveedor externo.** Recomendado: reutiliza MFA, recuperación y ciclo de vida de cuentas del proveedor, y permite que cada instalación elija uno. A cambio, incorpora OIDC al hub y el operador debe mantener un proveedor disponible; Google o GitHub añaden dependencia de terceros.
- **Usuario y contraseña propios con TOTP.** Esta alternativa queda reconsiderada y decidida en ADR-0023 con un alcance mucho menor: una sola credencial creada y rotada por CLI, sin registro ni recuperación por correo, sobre las sesiones existentes. El hub asume explícitamente la custodia y la superficie de ataque restante.
- **Delegar toda la identidad a un proxy.** Válido como patrón de despliegue y útil para Cloudflare Access, Authelia o un proxy corporativo. Se descarta como única garantía: es fácil dejar el origen expuesto, no ofrece un camino uniforme sin terceros y traslada seguridad crítica a una configuración que el hub no puede verificar.

## Consecuencias

### Positivas

- Las instalaciones pueden conservar control de identidad y MFA mediante un IdP autoalojado.
- Se evita construir un almacén de credenciales y flujo de recuperación propios.
- Cloudflare permanece opcional y el blindaje de origen queda documentado.

### Negativas

- OIDC añade configuración, claves de cliente, redirecciones y compatibilidad que el único mantenedor tendrá que mantener.
- No usar Cloudflare no elimina el trabajo del operador: deberá proteger la red y disponer de un IdP o proxy bajo su control.
- OIDC requiere un proveedor disponible; las instalaciones que dependan solo de él deben seguir planificando esa dependencia.

## Notas de implementación

- OIDC ya separa tokens de agentes y dispositivos de sesiones humanas; ADR-0023 extiende esa misma separación a la identidad local y al alcance por host.
- No aceptar tráfico público directo al origen cuando se use un proxy de identidad: aplicar firewall por rangos de Cloudflare o Cloudflare Tunnel, o el equivalente operado por la instalación.
- Documentar una ruta autoalojada sin Cloudflare antes de aceptar el ADR.
