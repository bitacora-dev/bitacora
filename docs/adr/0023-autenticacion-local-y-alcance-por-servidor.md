# ADR-0023: Autenticación local limitada y alcance por servidor

- **Estado:** Propuesto
- **Fecha:** 2026-09-19
- **Sustituye a:** parcialmente, ADR-0019 (solo el descarte de credenciales locales)

## Contexto

OIDC opcional de ADR-0019 ya está implementado en `internal/hubauth` y conserva
su valor para instalaciones con un IdP. Sin embargo, Cloudflare Access o el IdP
pueden degradarse. Si son la única puerta, el operador pierde el panel de
observabilidad precisamente durante una incidencia. Esa indisponibilidad es
evidencia nueva que ADR-0019 no valoró: hacer depender el acceso de Cloudflare
contradice el principio de no depender de terceros.

ADR-0019 descartó usuario y contraseña con TOTP por tener que custodiar hashes,
restablecimientos, MFA, sesiones y defensas ante ataques. Ese riesgo describe
un proveedor de identidad completo. La necesidad actual es menor: un operador,
una contraseña creada localmente por CLI y TOTP. No habrá registro, alta web ni
recuperación por correo. Las sesiones ya existen; se reutiliza esa maquinaria,
no se construye un segundo sistema de sesión.

Además, hoy la identidad humana no tiene alcance por host: `handleListHosts`
llama a `ListHosts` sin filtro y las rutas de lectura aceptan tokens de
dispositivo. Un futuro operador que comparte un host no debe poder descubrir
los demás. Esto es visibilidad, no solo permiso para actuar.

## Decisión

El hub acepta dos fuentes de identidad humana: OIDC y una credencial local.
Ambas producen una identidad canónica (`source`, `subject`) y crean la misma
sesión de `internal/hubauth`. OIDC no se elimina ni se degrada. La fuente local
inicial tiene un único sujeto, `local:operator`; no existe API ni interfaz web
para crear otros sujetos.

### Credencial local y operación

La credencial se guarda solo en `/etc/bitacora/local-auth.json`, propiedad de
`bitacora`, modo `0600`. Contiene el hash de contraseña, el secreto TOTP cifrado,
los hashes de códigos de recuperación, el contador de fallos, el instante de
bloqueo y una generación de sesión. La clave AES-256-GCM que cifra el secreto
TOTP vive en `/etc/bitacora/local-auth.key`, con el mismo propietario y modo.
No se guarda contraseña, secreto TOTP ni código de recuperación en claro.

La contraseña usa Argon2id con sal aleatoria de 16 bytes, 3 iteraciones, 64 MiB
de memoria, paralelismo 4 y salida de 32 bytes. Los códigos de recuperación se
guardan con el mismo algoritmo y parámetros. El TOTP sigue RFC 6238: SHA-1,
seis dígitos y período de 30 segundos; se acepta como máximo la ventana actual
y un período anterior o posterior. Al inicializar o regenerar TOTP, la CLI emite
exactamente diez códigos de recuperación aleatorios de 128 bits, de un solo uso.
Un código de recuperación sustituye al TOTP en un único inicio de sesión y se
elimina de forma atómica al aceptarse.

Solo la CLI local del servidor puede establecer o rotar la credencial:

```text
bitacora-hub auth local init
bitacora-hub auth local rotate-password
bitacora-hub auth local rotate-totp
bitacora-hub auth local regenerate-recovery-codes
```

Cada orden solicita la contraseña por TTY, nunca por argumento ni variable de
entorno; la orden de inicialización muestra el secreto TOTP una vez para su
alta en la aplicación autenticadora. Rotar contraseña o TOTP, regenerar códigos
de recuperación y deshabilitar la fuente local incrementa la generación y
revoca todas sus sesiones. El CLI deja un registro local de la operación sin
incluir secretos.

Después de cinco intentos fallidos consecutivos, el sujeto local queda bloqueado
durante 15 minutos; un acierto reinicia el contador. El bloqueo se persiste para
que reiniciar el hub no lo eluda. Una sesión humana dura 12 horas absolutas, no
se renueva por actividad, se invalida al cerrar sesión, al vencer, al reiniciar
el hub o al cambiar su generación de identidad.

No hay registro de usuarios, recuperación por correo, alta desde la web ni
restablecimiento remoto. Perder la contraseña, el segundo factor y los códigos
de recuperación exige acceso administrativo local al servidor para rotar la
credencial por CLI. Esta restricción es intencionada y no se ampliará sin otro
ADR.

### Alcance, visibilidad y rutas de lectura

La autorización humana se modela como una relación aditiva entre identidad y
host, con capacidades `view` y `operate`. La instalación inicial otorga al
único `local:operator` ambas capacidades sobre todos los hosts; no crea cuentas
ni una interfaz de asignación. Más adelante se podrán añadir identidades OIDC o
locales y relaciones por host sin cambiar el formato de sesión ni las rutas.

Toda solicitud humana de lectura resuelve primero la identidad de sesión y
después aplica `view` al host. `/v1/hosts` devuelve únicamente hosts visibles.
`/v1/summary`, `/v1/events`, `/v1/logs`, `/v1/inventory` y `/v1/jobs/` limitan
sus resultados a esos hosts y, cuando la ruta identifica un host fuera de
alcance, responden **404**, nunca 403. La misma regla se aplica a futuras rutas
de lectura. Los tokens de agente y dispositivo permanecen separados de las
sesiones humanas y no adquieren alcance humano por esta decisión.

### Relación con ADR-0022

Una operación de paquetes de ADR-0022 requiere, además de sus factores y lista
blanca, una identidad humana con `operate` sobre el host concreto. La auditoría
registra la identidad canónica, fuente, host, operación y solicitud. `view` no
autoriza una operación; un host que la identidad no puede operar se trata como
no visible y responde 404. El único operador inicial cumple ambos permisos para
todos sus hosts, pero el modelo no presupone que sea así en el futuro.

## Alternativas consideradas

- **Solo OIDC o Cloudflare Access.** Se descarta como única fuente: mantiene la
  carga de cuentas fuera del hub, pero deja al operador sin acceso cuando el
  proveedor o Cloudflare falla.
- **Proveedor local completo.** Se descarta: registro, correo, recuperación,
  administración web y soporte recrearían precisamente la carga de seguridad
  que ADR-0019 rechazó.
- **Usuario local sin TOTP.** Se descarta: reduce disponibilidad de la
  credencial, pero deja una contraseña como único factor ante un origen expuesto.
- **Permisos que devuelven 403.** Se descarta: revelan que el host existe y no
  satisfacen el requisito de no descubrir servidores ajenos.

## Consecuencias

### Positivas

- El operador conserva un camino local de acceso cuando Cloudflare o un IdP no
  están disponibles.
- OIDC sigue siendo una opción integrada para instalaciones que delegan cuentas
  y MFA en un IdP.
- El alcance por host queda definido antes de añadir varios operadores.

### Negativas

- El hub pasa a custodiar una credencial, una clave de cifrado y material de
  segundo factor; una vulnerabilidad en ese perímetro tiene consecuencias de
  autenticación que antes recaían en el IdP.
- La recuperación ante pérdida exige acceso administrativo local y puede dejar
  al operador temporalmente sin panel.
- La protección contra fuerza bruta, sesiones, criptografía y cambios de
  credencial requiere mantenimiento, pruebas y respuesta a vulnerabilidades.
- El alcance añade filtrado y pruebas a cada ruta de lectura y acción; omitir
  una ruta puede filtrar información de hosts.

## Notas de implementación

- Las tareas posteriores implementarán la fuente local, el adaptador común de
  sesión, el almacenamiento de alcance, la migración de rutas y las pruebas de
  no revelación. Este ADR no modifica código de producción ni habilita el login.
- Antes de cualquier implementación se revisarán los parámetros criptográficos
  frente a la biblioteca y la capacidad de memoria de la instalación; cualquier
  cambio de los valores decididos requiere actualizar este ADR.
- La guía de despliegue mantiene el blindaje del origen: autenticación local no
  convierte en seguro exponer sin más un origen público.
