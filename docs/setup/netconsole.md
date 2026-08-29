# Configurar netconsole

ADR-0011 usa netconsole para que el kernel envíe sus mensajes por UDP a
otra máquina en tiempo real. Si la máquina origen se congela, los
últimos mensajes ya están a salvo en el destino — es la única forma de
obtener mensajes del kernel de una máquina que ya no puede escribir en
su propio disco.

## En el hub (destino)

El receptor de Bitácora (`internal/netconsole`) escucha UDP en el puerto
configurado (por defecto `6666`). Asegúrate de que el firewall del hub
lo permite desde la red interna:

```
# ejemplo con nftables
nft add rule inet filter input udp dport 6666 accept
```

## En el host origen (p. ej. iCloudServer)

### Opción A: módulo dinámico vía configfs (recomendado, sin reiniciar)

```
modprobe netconsole
mkdir /sys/kernel/config/netconsole/target1
cd /sys/kernel/config/netconsole/target1

echo <interfaz>   > dev_name        # p. ej. eth0
echo <IP-origen>  > local_ip
echo <IP-destino> > remote_ip       # la IP del hub (UnRaid, en el caso de iCloudServer)
echo <MAC-destino> > remote_mac     # MAC del destino, o de la puerta de enlace si está en otra subred
echo 6666         > remote_port
echo 1            > extended        # formato extendido: incluye secuencia y timestamp
echo 1            > enabled
```

Comprueba que está activo:

```
cat /sys/kernel/config/netconsole/target1/enabled
```

### Opción B: parámetro de módulo (requiere `modprobe`/reinicio del módulo)

```
modprobe netconsole netconsole=@<IP-origen>/<interfaz>,6666@<IP-destino>/<MAC-destino>
```

## Hazlo persistente

La opción A no sobrevive a un reinicio por sí sola. Añade un fichero en
`/etc/modules-load.d/netconsole.conf` con `netconsole`, y un servicio
systemd (`oneshot`) que repita los `echo` de la opción A en el arranque
— o usa la opción B en `/etc/modules-load.d/` con el parámetro ya
incluido.

## Verifica

Desde el host origen:

```
logger -p kern.info "netconsole test message"
```

Y confirma que el hub lo recibe (revisa los logs de Bitácora, o
temporalmente con `nc -ul 6666` antes de apuntar el receptor real ahí).

## Notas

- netconsole no requiere confirmación de entrega: es UDP best-effort. Un
  paquete perdido no es un fallo del sistema, es la naturaleza del
  mecanismo — por eso se combina con pstore y la caja negra, no lo
  sustituye.
- El `host_id` de origen se resuelve por IP salvo que el hub tenga
  configurado un mapeo explícito — ver el README de
  `internal/netconsole`.
