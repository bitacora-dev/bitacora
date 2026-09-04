package storage

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

var hostMigrations = []string{
	`CREATE TABLE IF NOT EXISTS hosts (
		id            TEXT PRIMARY KEY,
		name          TEXT NOT NULL DEFAULT '',
		hostname      TEXT NOT NULL DEFAULT '',
		agent_version TEXT NOT NULL DEFAULT '',
		last_seen_at  INTEGER
	)`,
}

func scanHosts(rows *sql.Rows) ([]schema.Host, error) {
	defer rows.Close()
	var hosts []schema.Host
	for rows.Next() {
		var host schema.Host
		var lastSeenAt sql.NullInt64
		if err := rows.Scan(&host.ID, &host.Name, &host.Hostname, &host.AgentVersion, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scanning host: %w", err)
		}
		if lastSeenAt.Valid {
			host.LastSeenAt = time.UnixMilli(lastSeenAt.Int64).UTC()
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hosts: %w", err)
	}
	return hosts, nil
}
