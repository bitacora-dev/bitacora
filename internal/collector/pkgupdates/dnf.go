package pkgupdates

import (
	"encoding/json"

	"github.com/bitacora-dev/bitacora/internal/schema"
	"github.com/bitacora-dev/bitacora/internal/spool"
)

// dnfSpoolUpdate mirrors internal/dnfhelper.Update's JSON shape. It's
// duplicated here rather than importing dnfhelper directly, keeping this
// unprivileged collector decoupled from the privileged helper's package —
// the same separation internal/collector/diskarray uses for
// bitacora-smart's spool payload.
type dnfSpoolUpdate struct {
	Name    string `json:"name"`
	Arch    string `json:"arch"`
	Version string `json:"version"`
	Repo    string `json:"repo"`
}

// dnfItems reads bitacora-dnf's spool entry (ADR-0005) — the agent never
// runs `dnf check-update` itself.
func dnfItems(spoolDir string) []schema.InventoryItem {
	entries, err := spool.ReadDir(spoolDir)
	if err != nil {
		return nil
	}
	entry, ok := entries["dnfupdates"]
	if !ok {
		return nil
	}

	var result struct {
		Updates []dnfSpoolUpdate `json:"updates"`
	}
	if err := json.Unmarshal(entry.Data, &result); err != nil {
		return nil
	}

	items := make([]schema.InventoryItem, 0, len(result.Updates))
	for _, u := range result.Updates {
		if u.Name == "" {
			continue
		}
		items = append(items, schema.InventoryItem{
			ID:   "dnf:" + u.Name + "." + u.Arch,
			Name: u.Name,
			Attrs: schema.Labels{
				"source":            "dnf",
				"candidate_version": u.Version,
				"arch":              u.Arch,
				"repo":              u.Repo,
			},
		})
	}
	return items
}
