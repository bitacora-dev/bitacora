# Migración 0001: sondeo de jobs

Esta migración permite persistir un job en `running`, terminarlo una sola vez
y consultar de forma incremental sus líneas de salida. Las filas de jobs
terminales existentes se conservan sin cambios.

## UP

SQLite reconstruye `jobs` dentro de una transacción para convertir en
anulables `finished_at`, `duration_seconds` y `exit_code`; copia las columnas
históricas, añade `peer_host_id`, `trigger`, `next_expected` y
`log_refs_json`, crea el índice `(host_id, status, started_at DESC)` y crea:

```sql
CREATE TABLE job_output (
  job_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  stream TEXT NOT NULL,
  message TEXT NOT NULL,
  PRIMARY KEY (job_id, sequence),
  FOREIGN KEY (job_id) REFERENCES jobs(id)
);
```

PostgreSQL elimina los tres `NOT NULL`, añade las cuatro columnas con
`ADD COLUMN IF NOT EXISTS`, crea el mismo índice y `job_output` con
`BIGINT` para `sequence` y `ts`. El código aplica estas operaciones al abrir
el store; SQLite registra la versión en `job_schema_migrations`.

## DOWN

Antes de revertir, deben terminarse o eliminarse los jobs `running`; el
esquema previo no los puede representar. Después:

1. exportar o descartar `job_output` (la versión previa no tenía salida
   incremental);
2. eliminar `job_output` y el índice nuevo;
3. reconstruir SQLite `jobs` con el DDL anterior y copiar únicamente jobs
   terminales, restaurando `NOT NULL` en los tres campos; en PostgreSQL,
   eliminar las cuatro columnas añadidas y volver a aplicar los tres
   `SET NOT NULL` una vez comprobado que no quedan valores nulos;
4. bajar/eliminar la entrada de `job_schema_migrations`.

El DOWN es intencionalmente bloqueante ante jobs activos: convertirlos en
terminales inventaría un resultado y violaría el contrato de estados.
