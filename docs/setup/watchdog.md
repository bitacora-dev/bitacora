# Configurar el watchdog hardware

ADR-0011 documenta el watchdog como complementario, no alternativo: por
sí solo reinicia una máquina colgada, pero no explica por qué se colgó.
Combinado con la caja negra y pstore, sí lo hace — el watchdog garantiza
que la máquina vuelve, y los otros mecanismos capturan qué pasó justo
antes.

## 1. Carga el módulo del watchdog

La mayoría de placas Intel (incluida la del i9-13900K que motiva este
ADR) exponen el watchdog TCO del chipset:

```
modprobe iTCO_wdt
ls /dev/watchdog*
```

Si no aparece `/dev/watchdog`, comprueba qué watchdog expone tu
hardware:

```
dmesg | grep -i watchdog
```

## 2. Déjalo en manos de systemd

systemd puede "petear" el watchdog automáticamente mientras el sistema
esté vivo, y dejar de hacerlo (provocando el reinicio) si el propio
systemd se cuelga. En `/etc/systemd/system.conf`:

```ini
[Manager]
RuntimeWatchdogSec=30s
RebootWatchdogSec=10min
```

`RuntimeWatchdogSec` es el intervalo normal de "pet"; si systemd no lo
hace en ese tiempo (por estar colgado, no por estar simplemente
ocupado — systemd usa su propio hilo para esto), el hardware reinicia
la máquina. `RebootWatchdogSec` cubre el propio proceso de apagado, por
si se cuelga ahí.

Aplica el cambio:

```
systemctl daemon-reexec
```

## 3. Verifica

```
systemd-analyze dump | grep -i watchdog
```

Debería mostrar `RuntimeWatchdogUSec` con el valor configurado.

## Notas

- Un `RuntimeWatchdogSec` demasiado corto puede disparar falsos
  positivos si la máquina tiene picos de carga legítimos que retrasan al
  hilo de systemd — no hay un valor universal, empieza por 30s y ajusta
  si ves reinicios inesperados.
- El watchdog reinicia; no diagnostica. Confirma que pstore
  (`ramoops.md`) está activo para que el reinicio que provoca deje algo
  legible detrás.
