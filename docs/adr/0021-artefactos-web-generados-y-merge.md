# ADR-0021: Artefactos web generados y resolución de merges

- **Estado:** Aceptado
- **Fecha:** 2026-09-08

## Contexto

El hub incluye `internal/webui/dist` mediante `go:embed`. ADR-0001 mantiene el
artefacto de Vite versionado para que construir los binarios de Go no exija
instalar Node ni ejecutar una compilación web. Sin embargo, una regeneración de
Vite cambia nombres con hash y contenido generado que no admite una resolución
manual útil cuando dos ramas modifican el frontend.

## Decisión

Se conserva `internal/webui/dist` en Git. El patrón
`internal/webui/dist/**` declara el driver de merge
`bitacora-ours-dist` en `.gitattributes`. Cada desarrollador lo activa por
checkout con `./scripts/git/configure-merge-drivers.sh`, que escribe solo en
`.git/config` el comando versionado `scripts/git/merge-ours-dist.sh %O %A %B`.

Cuando ambas ramas alteran un archivo de `dist`, el driver acepta la versión de
la rama actual (`ours`) y evita un conflicto de contenido generado. Tras el
merge se ejecuta `cd web && npm ci && npm run build`; el resultado regenerado se
revisa y se añade al commit de merge si cambia. CI aplica exactamente esa
regeneración y falla si `git diff --exit-code -- internal/webui/dist` detecta
que el artefacto versionado está desactualizado.

La activación no se oculta en un hook ni en configuración global: Git no
versiona drivers de merge por seguridad, y una acción explícita permite al
contribuidor inspeccionar y repetir la configuración de su clon.

## Alternativas consideradas

- **No versionar `dist` y construirlo en cada build de Go.** Se descarta porque
  contradice ADR-0001 y obliga a instalar Node en consumidores del binario.
- **Resolver cada conflicto generado manualmente.** Se descarta porque los
  hashes y el contenido de Vite no expresan una decisión humana; regenerar es
  más fiable.
- **Configurar el driver globalmente.** Se descarta porque afectaría repositorios
  ajenos y no sería una configuración reproducible del proyecto.

## Consecuencias

### Positivas

- Los binarios de Go continúan siendo construibles sin Node.
- Los merges de artefactos generados terminan limpios y siempre se validan por
  regeneración en CI.
- El script y una prueba aislada dejan verificable la configuración local.

### Negativas

- Cada clon debe activar una vez el driver.
- `ours` puede descartar provisionalmente el artefacto de la otra rama; la
  regeneración posterior al merge es obligatoria para recuperar el resultado
  canónico.
