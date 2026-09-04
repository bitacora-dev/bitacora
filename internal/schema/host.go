package schema

import "time"

// Host is the latest identity and availability record for one enrolled
// agent. Name is operator supplied, while Hostname and AgentVersion are
// reported by the authenticated agent manifest.
type Host struct {
	ID           string    `json:"id"`
	Name         string    `json:"name,omitempty"`
	Hostname     string    `json:"hostname,omitempty"`
	AgentVersion string    `json:"agent_version,omitempty"`
	LastSeenAt   time.Time `json:"last_seen_at,omitempty"`
}
