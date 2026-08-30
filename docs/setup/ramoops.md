# Configurar pstore/ramoops

ADR-0011 usa `/sys/fs/pstore` para recuperar el oops o panic de un cuelgue
que no llegó a escribirse en el journal. En muchas máquinas esto ya
funciona sin tocar nada; en otras hace falta reservar memoria a mano.

## 1. Comprueba si ya funciona

```
mount | grep pstore
ls /sys/fs/pstore
```

Si `/sys/fs/pstore` existe y está montado, probablemente ya tienes un
backend funcionando sin configuración:

- **UEFI**: el backend `efi_pstore` usa variables EFI y está activo por
  defecto en la mayoría de kernels Linux en máquinas UEFI. No requiere
  ningún parámetro.
- **ACPI ERST**: algunos servidores exponen una región de error
  persistente vía ACPI (`erst`), también sin configuración.

Si `bita doctor` reporta pstore como disponible, no hay nada más que
hacer en esta guía.

## 2. Si no hay backend disponible: ramoops manual

`ramoops` reserva una región fija de RAM que sobrevive a un reinicio
(pero no a un corte de corriente, salvo con RAM persistente real).
Requiere pasar la dirección y tamaño de esa región como parámetro de
arranque del kernel.

Edita la línea de kernel de GRUB (`/etc/default/grub`,
`GRUB_CMDLINE_LINUX`) y añade:

```
ramoops.mem_address=0x90000000 ramoops.mem_size=0x200000 ramoops.record_size=0x8000 ramoops.console_size=0x8000
```

- `mem_address`: una dirección física libre, fuera del rango que el
  kernel usa normalmente. Consulta `cat /proc/iomem` para encontrar un
  hueco; en máquinas con mucha RAM, reservar cerca del final del rango
  físico suele ser seguro.
- `mem_size`: tamaño total de la región (2 MiB en el ejemplo).
- `record_size`/`console_size`: cuánto se dedica a cada volcado de dmesg
  y a la consola.

Regenera la configuración de GRUB (`update-grub` o `grub2-mkconfig`,
según la distribución) y reinicia.

## 3. Verifica tras el reinicio

```
mount | grep pstore
```

Y provoca un panic de prueba de forma controlada si quieres validar el
camino completo:

```
echo c > /proc/sysrq-trigger
```

(Esto **reinicia la máquina** — solo en un entorno de prueba.) Tras el
reinicio, `/sys/fs/pstore/dmesg-*` debería contener el volcado, y el
agente lo consumirá y lo limpiará en el siguiente arranque
(`internal/pstore`).

## Notas

- El espacio de ramoops es pequeño y finito: el agente borra cada
  entrada tras consumirla (ADR-0011) para dejar sitio al siguiente
  suceso.
- Si la máquina no tiene una región de memoria segura para reservar
  (poca RAM, firmware restrictivo), pstore no es viable y hay que
  depender de netconsole (`netconsole.md`) como alternativa.
