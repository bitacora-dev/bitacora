# Blindaje del origen con Cloudflare

Esta guía protege un hub de Bitácora expuesto a Internet para que su origen no acepte tráfico directo. Cloudflare es **opcional**: Bitácora sigue siendo autoalojable sin Cloudflare y sin servicios externos obligatorios.

Use uno de estos patrones. Cloudflare Tunnel es el más sencillo para no abrir puertos entrantes; el firewall por rangos es adecuado cuando ya se usa el proxy de Cloudflare.

> Mantenga una sesión administrativa abierta y pruebe el acceso después de cada cambio. Una regla incorrecta puede bloquear el host remoto.

## Patrón A: proxy de Cloudflare y firewall por rangos IP

Este patrón mantiene el puerto HTTPS abierto solo para las IP de Cloudflare.

### Pasos

1. Active el proxy de Cloudflare para el registro DNS del hub (nube naranja).
2. Configure el proxy inverso local para servir HTTPS hacia el hub en el puerto que use su despliegue.
3. Consulte los [rangos IP oficiales de Cloudflare](https://www.cloudflare.com/ips/) justo antes de crear las reglas. No copie una lista antigua de esta guía ni los fije en scripts del proyecto.
4. En el firewall del host o de la red, permita TCP al puerto HTTPS únicamente desde esos rangos IPv4 e IPv6. Mantenga SSH o su canal de administración con una regla separada y restringida.
5. Elimine la regla pública que permita ese puerto desde cualquier dirección.
6. Desde una red externa, compruebe que el nombre público responde a través de Cloudflare y que conectar a la IP pública del origen ya no responde.

### Verificación

- El DNS público muestra el proxy de Cloudflare activo.
- La aplicación responde mediante el nombre público esperado.
- Una conexión directa a la IP del origen y al puerto publicado falla.
- Los registros del firewall muestran únicamente orígenes de Cloudflare para ese puerto.

### Mantenimiento

Los rangos de Cloudflare pueden cambiar. Revise la fuente oficial al modificar las reglas y defina en su propia operación cómo auditar esa lista. Si no puede mantenerla con seguridad, use el patrón de túnel o acceso por VPN.

## Patrón B: Cloudflare Tunnel sin puertos entrantes

Este patrón establece una conexión saliente desde el host a Cloudflare. El firewall no publica el puerto del hub a Internet.

### Pasos

1. Cree y autentique un túnel en la cuenta de Cloudflare siguiendo su documentación oficial.
2. Configure el túnel para dirigir el nombre público al proxy local o al puerto local del hub. El servicio del hub debe escuchar solo en loopback o en una red privada cuando sea posible.
3. Cree el registro DNS público asociado al túnel.
4. Retire las reglas de entrada pública para HTTP/HTTPS en el firewall. Permita solo las salidas que necesite el conector del túnel.
5. Ejecute el conector como servicio supervisado por el sistema operativo y guarde sus credenciales según la política local de secretos.
6. Desde una red externa, confirme que el nombre público funciona y que la IP pública del host no acepta conexiones a los puertos del hub.

### Verificación

- El conector informa un túnel saludable.
- El hub no está enlazado a una interfaz pública, salvo que una red local lo requiera expresamente.
- No hay reglas de entrada pública para el puerto HTTP/HTTPS del hub.
- El acceso público funciona solo mediante el nombre asociado al túnel.

## Sin Cloudflare

No hay degradación funcional si no se usa Cloudflare. Para una instalación puramente autoalojada, priorice una VPN privada (por ejemplo, Tailscale), un firewall restrictivo y un proxy inverso operado por usted. Si decide exponer el hub directamente, asuma que la IP del origen es pública y aplique autenticación, TLS, actualizaciones y controles de red propios.

## Límites y responsabilidad

Cloudflare no sustituye la autenticación del hub ni el endurecimiento del host. El operador decide si acepta el intercambio entre ocultar el origen y depender de un tercero. Bitácora no crea reglas, no almacena tokens de Cloudflare y no soporta la automatización de esta configuración.
