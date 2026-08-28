# ADR-0013: Nombre, licencia y gobernanza

- **Estado:** Aceptado
- **Fecha:** 2026-08-28

## Contexto

El repositorio es público desde el primer commit. Tres decisiones se toman una
sola vez y son caras o imposibles de revertir: el nombre, la licencia y el
espacio de nombres en los registros públicos.

Este ADR se redactó inicialmente como propuesta con *Atalaya* como nombre
candidato. La verificación de disponibilidad cambió la conclusión. Se documenta
el proceso porque el criterio es reutilizable.

## Decisión

### Nombre: Bitácora

El cuaderno de a bordo donde se registra lo ocurrido. Describe el producto mejor
que cualquier alternativa evaluada: no es solo vigilancia en vivo, es poder
reconstruir qué pasó a una hora concreta cruzando CPU, memoria, discos,
servicios, backups y eventos.

**Nomenclatura:**

| Elemento | Valor |
|---|---|
| Marca | Bitácora |
| Organización GitHub | `bitacora-dev` |
| Repositorio | `github.com/bitacora-dev/bitacora` |
| Agente | `bitacora-agent` |
| Hub (ingesta + almacenamiento + alertas + API + UI) | `bitacora-hub` |
| Wrapper de jobs | `bitacora-run` |
| CLI de administración | `bita` |
| Usuario de sistema | `bitacora` |
| Rutas | `/etc/bitacora`, `/var/lib/bitacora` |
| Prefijo de métricas | `bitacora_` |
| Documentación | `bitacora.nacasoweb.es` (CNAME a GitHub Pages) |

La tilde existe únicamente en la marca. Todos los identificadores van en ASCII
sin acento. Es una discrepancia asumida conscientemente.

### Registro de contenedores: GHCR, no Docker Hub

```
ghcr.io/bitacora-dev/bitacora-hub
ghcr.io/bitacora-dev/bitacora-agent
```

Motivo: **Docker Hub eliminó las organizaciones gratuitas.** A fecha de esta
decisión, crear una organización exige el plan Team, con un mínimo de 180 USD
anuales. Es un gasto injustificable para un proyecto sin ingresos.

GHCR es además preferible por mérito propio para este caso:

- Espacio de nombres ya reservado con la organización de GitHub.
- Sin límites de descarga para repositorios públicos, a diferencia de Docker Hub,
  cuyas restricciones para usuarios anónimos son cada vez mayores.
- Publicación desde GitHub Actions sin gestionar credenciales de un tercero.
- Imágenes y código en el mismo sitio, con la misma política de acceso.

Contrapartida: el comando de descarga es más largo
(`docker pull ghcr.io/bitacora-dev/bitacora-hub`). Se resuelve poniéndolo
completo en el README, que es donde la gente lo copia.

Si en el futuro interesa visibilidad en Docker Hub, se replica bajo una cuenta
personal, que sí es gratuita. No es prioritario.

### Verificación de disponibilidad realizada

Comprobado el 2026-08-28. El método correcto en GitHub es visitar
`github.com/<nombre>` y leer el código HTTP (404 libre, 200 ocupado), porque
usuarios y organizaciones **comparten un único espacio de nombres**: buscar
repositorios no sirve.

| Nombre | GitHub | Docker Hub |
|---|---|---|
| `bitacora` | ocupado | ocupado |
| `almenara` | ocupado | ocupado |
| `atalaya` | ocupado | — |
| `singladura` | libre | libre |
| **`bitacora-dev`** | **libre → registrado** | — |

**Por qué se descartó Atalaya**, que era la propuesta inicial: existe
`AtalayaLabs`, una organización activa que publica software open source
autoalojado de infraestructura (OxiCloud), con actividad reciente. Mismo nicho y
mismo público. Es riesgo de confusión real, no teórico, y potencial fricción de
marca a futuro. Existen además `atalaya-io` y una empresa con el dominio
`atalaya.io`.

**Por qué se descartó Almenara**: ocupada en GitHub y en Docker Hub.

**Por qué se descartó Singladura**, pese a estar libre en todas partes: es una
palabra que hay que deletrear cada vez que se menciona. El sufijo `-dev` en la
cuenta de GitHub se ve una vez, en el `git clone`, y nunca más. Una molestia
puntual es mejor negocio que una fricción permanente.

**Sobre el dominio**: no se compra ninguno. Todos los TLD razonables de
`bitacora` están registrados, y el `.com` está aparcado por un revendedor a un
precio de fantasía. Un proyecto open source vive en GitHub y sirve su
documentación desde GitHub Pages; el dominio es el criterio menos importante de
los cuatro evaluados. Se usa un subdominio de un dominio ya existente, sin coste
adicional.

### Licencia: dual

| Componente | Licencia |
|---|---|
| `bitacora-hub`, `bitacora-agent`, `bita`, frontend | **AGPL-3.0** |
| `bitacora-run` | **Apache-2.0** |

**Por qué AGPL-3.0 en el núcleo.** La AGPL cierra el hueco que deja la GPL
clásica: obliga a publicar las modificaciones no solo al distribuir el software,
sino también al ofrecerlo como servicio a terceros. Sin ella, cualquiera puede
tomar este código, añadirle funciones, cerrarlo, venderlo como SaaS y no devolver
nada. Dado que se trata precisamente de una herramienta que se ofrecería de forma
natural como servicio, es la protección pertinente.

**Coste asumido, sin adornos:** la AGPL reduce la adopción empresarial. Muchas
organizaciones la prohíben por política interna. Si el objetivo prioritario fuera
la máxima difusión, Apache-2.0 sería la elección correcta. No lo es: el objetivo
es que lo construido siga siendo libre para todos.

**Por qué Apache-2.0 en `bitacora-run`.** Es el wrapper que la gente querrá
anteponer en sus crontabs y en infraestructura corporativa. Es probablemente el
mejor gancho de adopción del proyecto (ADR-0010). Una licencia permisiva ahí
maximiza su difusión sin ceder nada sobre el núcleo, que es donde reside el
trabajo de diseño.

**Nota sobre reversibilidad:** al ser un único titular de derechos, relicenciar
de AGPL a Apache más adelante es posible en cualquier momento. Al revés no lo es,
porque quien haya hecho fork conserva los derechos de la licencia original.
Empezar restrictivo y abrir después es la jugada segura.

Cada directorio con licencia distinta lleva su propio `LICENSE` y una cabecera
que lo indica sin ambigüedad.

### Idioma

| Contenido | Idioma |
|---|---|
| Código, identificadores, comentarios | Inglés |
| Mensajes de commit | Inglés |
| `README.md`, documentación de usuario | Inglés |
| Issues, PR, plantillas | Inglés |
| **ADR (`docs/adr/`)** | **Español** |

Razonamiento: el inglés es la barrera de entrada más baja para contribuidores
externos, que es un objetivo declarado. Los ADR, en cambio, son ahora mismo un
instrumento de trabajo y de decisión del mantenedor; forzar el inglés en esta
fase resta precisión al razonamiento sin beneficiar a nadie todavía. Se traducen
cuando el proyecto se anuncie públicamente y haya quien los lea.

Los mensajes de commit van en inglés **desde el primer commit**, porque es lo
único que no se puede reescribir después sin destrozar el historial.

### Gobernanza y flujo de trabajo

- Ramas: `task/*` → PR → `development` → `main`.
- `development` es integración y staging. `main` es estable y etiquetada.
- **Ningún agente de IA hace merge.** El merge lo hace una persona, siempre.
- Toda PR de un agente indica: qué ADR aplica, qué se ha probado y qué se ha
  dejado fuera deliberadamente.
- Se fomenta la revisión cruzada entre agentes (Claude implementa y Codex audita,
  o al revés), sin roles fijos.
- Protección de rama en `main` y `development`: sin push directo, CI verde
  obligatorio, al menos una aprobación humana.

Ajustes de la organización de GitHub, aplicados desde el principio:

- 2FA obligatorio para todos los miembros.
- Creación de repositorios restringida a propietarios.
- Plan Free (repositorios ilimitados, Actions gratis en público, Pages incluido).

### Higiene del repositorio público

El repositorio **nunca** contiene contraseñas, tokens, dominios privados, IP
privadas obligatorias, credenciales ni datos personales.

Medidas mecánicas, no confiadas a la disciplina:

- `gitleaks` en CI, bloqueante.
- Hook de pre-commit con `gitleaks` y `detect-secrets`.
- Toda configuración específica de instalación en `/etc/bitacora/` o en variables
  de entorno, jamás en el código.
- `config.example.yaml` con valores manifiestamente ficticios (`192.0.2.x`,
  `example.invalid`, rangos reservados por RFC para documentación).
- Los fixtures de test se **anonimizan con un script versionado**, no a mano: los
  volcados de `/proc` y de logs reales contienen hostnames, rutas de usuario y a
  veces direcciones IP.

### Ficheros obligatorios desde el primer commit

`README.md`, `LICENSE` (AGPL-3.0 en raíz), `CONTRIBUTING.md`, `SECURITY.md` (con
el modelo de amenazas y el límite del ADR-0012), `CODE_OF_CONDUCT.md`,
`CHANGELOG.md` (formato Keep a Changelog), `.gitleaks.toml`, y plantillas de
issue y de PR.

## Alternativas consideradas

- **Docker Hub como registro principal.** Descartado por coste: 180 USD anuales
  por una organización, más límites de descarga crecientes.
- **Licencia única Apache-2.0 para todo.** Descartado: permitiría cerrar el
  núcleo sin contrapartida.
- **Licencia única AGPL-3.0 para todo.** Descartado: penalizaría la adopción de
  `bitacora-run`, que es la pieza con mayor potencial de difusión y la que menos
  diseño propio contiene.
- **Todo en español, incluido el repositorio.** Descartado: cierra la puerta a
  contribuidores externos, que es un objetivo explícito del proyecto.
- **Comprar un dominio propio.** Descartado por coste sin beneficio: la
  documentación se sirve igual desde un subdominio ya disponible.

## Consecuencias

### Positivas
- Coste económico del proyecto: **cero euros**. Sin dominio, sin registro de
  contenedores de pago, sin plan de GitHub de pago.
- Los nombres quedan asegurados en los dos sitios que importan.
- La AGPL garantiza que las mejoras vuelvan al proyecto.

### Negativas
- La AGPL cerrará puertas en entornos corporativos con política contraria.
- La licencia dual obliga a mantener claros los límites entre módulos y a
  documentarlo en cada directorio. Un fichero que cruce esa frontera es un error
  de licenciamiento silencioso.
- El sufijo `-dev` en la organización y la ruta larga de GHCR son fricciones
  menores pero permanentes.
- Escribir en dos idiomas tiene coste de mantenimiento y riesgo de divergencia
  entre los ADR y la documentación pública.

## Notas de implementación

- Añadir el `LICENSE` al repositorio **antes** del primer commit con código real.
  Un repositorio sin licencia es técnicamente "todos los derechos reservados", lo
  cual es aceptable mientras no haya nada que licenciar, pero no después.
- CI debe verificar que cada directorio bajo licencia Apache no importe código
  AGPL. La separación tiene que ser mecánica.
- El README declara explícitamente el modelo de licencia dual, para que nadie
  tenga que deducirlo.
