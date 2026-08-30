package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

// inventoryMigrations is SQLite-specific: unlike events, an Inventory has
// no time dimension worth sharding by month — it's a latest-only snapshot
// per (host_id, kind), so it lives in its own single, unsharded file
// rather than going through monthDB.
var inventoryMigrations = []string{
	`CREATE TABLE IF NOT EXISTS inventories (
		host_id     TEXT NOT NULL,
		kind        TEXT NOT NULL,
		reported_at INTEGER NOT NULL,
		schema      INTEGER NOT NULL,
		items_json  TEXT NOT NULL,
		PRIMARY KEY (host_id, kind)
	)`,
}

// rowScanner is the common surface of *sql.Row and *sql.Rows that
// scanInventory needs — lets SQLite and PostgreSQL share one scan
// function despite querying through different row types.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanInventory(row rowScanner) (schema.Inventory, bool, error) {
	var (
		inv          schema.Inventory
		kind         string
		reportedAtMs int64
		itemsJSON    string
	)
	err := row.Scan(&inv.HostID, &kind, &reportedAtMs, &inv.Schema, &itemsJSON)
	if err == sql.ErrNoRows {
		return schema.Inventory{}, false, nil
	}
	if err != nil {
		return schema.Inventory{}, false, fmt.Errorf("scanning inventory row: %w", err)
	}

	inv.Kind = schema.InventoryKind(kind)
	inv.ReportedAt = time.UnixMilli(reportedAtMs).UTC()
	if err := json.Unmarshal([]byte(itemsJSON), &inv.Items); err != nil {
		return schema.Inventory{}, false, fmt.Errorf("unmarshaling inventory items: %w", err)
	}
	return inv, true, nil
}
