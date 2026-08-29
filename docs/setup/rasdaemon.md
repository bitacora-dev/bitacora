# Configurar rasdaemon

ADR-0011 recomienda `rasdaemon` como insumo, no como sustituto del
análisis propio: se instala y se ingiere su base de datos, pero no da la
correlación con la topología de CPU ni la prueba estadística — eso es lo
que aporta `internal/faultcluster`.

## 1. Instala

```
# Debian/Ubuntu
apt install rasdaemon

# AlmaLinux/RHEL
dnf install rasdaemon
```

## 2. Habilita el registro en SQLite

Comprueba que se compiló con soporte SQLite (la mayoría de paquetes de
distribución lo traen):

```
rasdaemon --version
```

Edita `/etc/sysconfig/rasdaemon` (RHEL/Alma) o el fichero de entorno
equivalente y confirma que `RASDAEMON_ARGS` no desactiva la base de
datos. Por defecto, rasdaemon guarda los eventos en:

```
/var/lib/rasdaemon/ras-mc_event.db
```

## 3. Arranca el servicio

```
systemctl enable --now rasdaemon
```

## 4. Verifica

```
ras-mc-ctl --summary
```

Debería listar los controladores de memoria detectados y su conteo de
errores (normalmente cero en una máquina sana).

## Qué ingiere Bitácora

El agente lee `ras-mc_event.db` (errores EDAC — corregibles y no
corregibles — y, si el hardware los reporta, errores MCE) además de leer
directamente los contadores en
`/sys/devices/system/edac/mc/mc*/{ce_count,ue_count}` para la caja
negra (ADR-0011). rasdaemon da el histórico detallado y decodificado;
los contadores de sysfs dan la serie temporal barata que va en cada
muestra de 1 Hz.

## Notas

- Sin ECC en la RAM, rasdaemon no tiene nada que reportar sobre memoria
  — sigue siendo útil para errores de PCIe/otros buses si el hardware
  los expone.
- Un incremento repentino de `ce_count` (errores corregibles) sin
  errores no corregibles todavía es exactamente el tipo de señal
  temprana que este ADR busca capturar antes de que se convierta en un
  fallo duro.
