# ADR-0025: Instalación y recuperación local de la credencial

- **Estado:** Propuesto
- **Fecha:** 2026-09-20
- **Sustituye a:** parcialmente, ADR-0023 (solo la cláusula que prohíbe alta web y restablecimiento remoto)

## Contexto

ADR-0023 define una única identidad local estable, `local:operator`, con contraseña, TOTP, diez códigos de recuperación, bloqueo persistido y revocación de sesiones mediante generación. También prohíbe el alta web y el restablecimiento remoto: el flujo inicial exige que una persona establezca la credencial desde la CLI local.

Esa prohibición impide completar una instalación nueva sin introducir una credencial por CLI y no ofrece un flujo de recuperación cuando se pierden a la vez el usuario, la contraseña, TOTP y los códigos. Se necesita permitir esos dos casos sin convertir Bitácora en un proveedor de identidad ni abrir una vía de recuperación remota.

No se usarán credenciales por defecto, incluido `admin`/`admin`. Una credencial conocida crea una ventana de ataque desde el primer arranque hasta que el operador la sustituye. Obligar a cambiarla en el primer inicio de sesión no cierra esa ventana: quien la use antes puede crear una sesión o cambiar la credencial. La instalación debe empezar sin credencial utilizable.

## Decisión

Se enmienda **solo** la cláusula de ADR-0023 que prohíbe alta web y restablecimiento remoto. El resto de ADR-0023 —incluidos sujeto único, almacenamiento criptográfico, TOTP, diez códigos de recuperación, bloqueo, sesiones, generación, alcance por host y separación de OIDC— permanece vigente.

### Instalación inicial

Cuando el hub arranca sin una credencial local inicializada, genera un token de instalación aleatorio, de un solo uso y con vencimiento. Persiste únicamente su hash, su instante de expiración y su estado de consumo; nunca persiste ni vuelve a mostrar el token en claro. Un token vencido o consumido se rechaza y no puede inicializar la identidad.

El token se entrega exclusivamente por una consola, log o archivo cuya lectura ya requiere acceso administrativo local al servidor. No hay endpoint, correo, API, interfaz ni otro mecanismo remoto para emitirlo, recuperarlo o reenviarlo. Emitir un token a distancia destruiría la frontera de autenticación: permitiría iniciar o recuperar la única identidad humana sin la posesión local que este diseño exige.

La interfaz web de instalación acepta el token y solicita un nombre de usuario libre, una contraseña y el alta TOTP; no exige ni trata el nombre de usuario como correo electrónico. Tras validar el token, crea o actualiza la credencial de ADR-0023, genera exactamente diez códigos de recuperación de un solo uso y consume el token de forma atómica. El nombre de usuario no crea otro sujeto: solo es el atributo de inicio de sesión y de presentación de `local:operator`.

### Recuperación por pérdida total

La recuperación «olvidé el usuario y la contraseña» solo se inicia mediante un comando administrativo local ejecutado dentro del runtime apropiado del hub. El comando genera un token de recuperación aleatorio, de un solo uso y con vencimiento, persiste solo su hash y estado, e invalida inmediatamente cualquier token de recuperación anterior. La entrega del token sigue las mismas vías locales protegidas de la instalación inicial.

El comando no revela el nombre de usuario. Al canjear el token en la interfaz, el operador puede reemplazar el nombre de usuario, la contraseña, TOTP y los diez códigos de recuperación. El canje consume el token de forma atómica, incrementa la generación de `local:operator` y revoca todas sus sesiones. No hay correo, enlace enviado por red, endpoint de activación, botón que emita el token ni restablecimiento iniciado desde la web.

La pérdida de TOTP con contraseña todavía conocida no es una pérdida total: se resuelve con uno de los diez códigos de recuperación que ADR-0023 define. La recuperación local de este ADR queda reservada para perder también la contraseña, el usuario o los códigos; no sustituye el uso ordinario de esos códigos.

### Identidad, auditoría y persistencia en contenedores

El sujeto de auditoría estable sigue siendo `local:operator`, aunque el nombre de usuario de login sea editable. El nombre de usuario es un atributo mutable y controlado para presentación e inicio de sesión, no una identidad canónica. La auditoría aplica ADR-0022: registra el actor o evento del plano de control, el host y el resultado, pero no guarda tokens, contraseñas, secretos TOTP ni códigos de recuperación. No registra el nombre de usuario salvo que sea estrictamente necesario y se aplique minimización.

Las rutas de estado de credencial y clave deben ser configurables (por flags o variables de entorno; el mecanismo concreto se decide al implementar). Todo despliegue en contenedor debe documentar y montar obligatoriamente un volumen persistente que cubra ambos estados: la credencial y la clave que protege el secreto TOTP. Arrancar sin ese volumen es una configuración de despliegue incorrecta, no un modo compatible ni una recuperación automática.

## Alternativas consideradas

- **`admin`/`admin` y cambio obligatorio al primer login.** Se descarta: la credencial conocida es explotable antes del cambio y el requisito posterior no elimina sesiones ni acciones realizadas durante esa ventana.
- **Mantener exclusivamente la CLI para la instalación y la recuperación.** Se descarta: conserva la frontera local, pero impone entrada de secretos y enrolamiento TOTP por CLI cuando la interfaz ya puede completar esos pasos tras probar posesión mediante un token local.
- **Correo, enlace de recuperación o emisión web de tokens.** Se descarta: añade dependencias, entrega remota de control y una superficie de recuperación que equivale a una segunda puerta de autenticación.
- **Usar el nombre de usuario mutable como sujeto de auditoría.** Se descarta: rompe la correlación histórica cuando el login cambia y permite que un cambio de presentación parezca un actor distinto.
- **Estado efímero en el contenedor.** Se descarta: una recreación perdería credenciales o claves y podría forzar reinicializaciones inseguras.

## Consecuencias

### Positivas

- Una instalación nueva no expone ninguna contraseña conocida por defecto.
- El alta inicial y la recuperación completa pueden terminarse en la web sin perder la prueba de acceso administrativo local al servidor.
- La auditoría conserva una identidad estable aunque el login sea reemplazado.
- El volumen explícito hace visible el requisito de persistencia de seguridad en despliegues de contenedor.

### Negativas

- Perder el acceso administrativo local al servidor elimina toda vía de recuperación: solo queda reconstruir, redesplegar o reinicializar la instalación según el procedimiento operativo aplicable.
- La consola, los logs y el archivo de entrega pasan a ser material sensible; sus permisos, retención y acceso local deben protegerse.
- La recuperación revoca todas las sesiones y requiere volver a enrolar TOTP y distribuir nuevos códigos, lo que introduce interrupción deliberada.
- Las rutas configurables y el volumen persistente aumentan las decisiones de despliegue; un volumen ausente o mal montado es un error operativo que debe detectarse y corregirse.

## Notas de implementación

- Implementar almacenamiento hash+expiración+consumo para los tokens de instalación y recuperación, con consumo e invalidación atómicos.
- Definir el comando administrativo local y validar que solo se ejecute dentro del runtime adecuado; nunca aceptar secretos o tokens por argumentos o variables de entorno.
- Añadir las pantallas de instalación y canje, el reemplazo de credencial y la revocación por generación sin crear alta de usuarios ni activación remota.
- Documentar las flags o variables de entorno de rutas y el volumen persistente obligatorio que contiene estado de credencial y clave.
- Cubrir auditoría, expiración, uso único, invalidación del token previo, pérdida de TOTP con contraseña conocida y pérdida total con pruebas.
