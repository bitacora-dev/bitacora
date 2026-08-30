package pkgupdates

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bitacora-dev/bitacora/internal/schema"
)

var (
	entityDeclRe = regexp.MustCompile(`<!ENTITY\s+(\w+)\s+["']([^"']*)["']\s*>`)
	pluginTagRe  = regexp.MustCompile(`(?s)<PLUGIN\b([^>]*)>`)
	attrRe       = regexp.MustCompile(`(\w+)\s*=\s*["']([^"']*)["']`)
)

// plgInfo is the handful of attributes this needs out of an UnRaid .plg
// file's root <PLUGIN> tag — name, installed version, and the URL where
// the plugin's own up-to-date definition lives. Real .plg files often
// reference these via DOCTYPE <!ENTITY> macros (&name;, &version;, ...)
// rather than literal attribute values; encoding/xml doesn't resolve
// DTD-internal entities, so this resolves them itself with two regex
// passes rather than pulling in a full XML+DTD parser for three fields.
type plgInfo struct {
	Name      string
	Version   string
	PluginURL string
}

func parsePLG(data []byte) (plgInfo, bool) {
	entities := map[string]string{}
	for _, m := range entityDeclRe.FindAllSubmatch(data, -1) {
		entities[string(m[1])] = string(m[2])
	}

	tag := pluginTagRe.FindSubmatch(data)
	if tag == nil {
		return plgInfo{}, false
	}

	attrs := map[string]string{}
	for _, m := range attrRe.FindAllSubmatch(tag[1], -1) {
		attrs[string(m[1])] = resolveEntity(string(m[2]), entities)
	}

	info := plgInfo{Name: attrs["name"], Version: attrs["version"], PluginURL: attrs["pluginURL"]}
	if info.Name == "" || info.Version == "" {
		return plgInfo{}, false
	}
	return info, true
}

func resolveEntity(value string, entities map[string]string) string {
	if strings.HasPrefix(value, "&") && strings.HasSuffix(value, ";") && len(value) > 2 {
		if resolved, ok := entities[value[1:len(value)-1]]; ok {
			return resolved
		}
	}
	return value
}

// unraidItems reads every locally installed plugin's .plg definition and,
// for each one that declares a pluginURL, fetches the same file from its
// own source to compare — UnRaid keeps no local "what's available" cache
// the way apt/dnf do, so each plugin is its own remote round-trip. A
// plugin whose source doesn't respond, times out, or doesn't parse is
// skipped for this cycle, not fatal to the others.
func unraidItems(ctx context.Context, pluginsDir string, client *http.Client) []schema.InventoryItem {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}

	var items []schema.InventoryItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plg") {
			continue
		}

		local, err := os.ReadFile(filepath.Join(pluginsDir, e.Name()))
		if err != nil {
			continue
		}
		info, ok := parsePLG(local)
		if !ok || info.PluginURL == "" {
			continue
		}

		remoteVersion, ok := fetchRemoteVersion(ctx, client, info.PluginURL)
		if !ok || remoteVersion == info.Version {
			continue
		}

		items = append(items, schema.InventoryItem{
			ID:   "unraid_plugin:" + info.Name,
			Name: info.Name,
			Attrs: schema.Labels{
				"source":            "unraid_plugin",
				"installed_version": info.Version,
				"candidate_version": remoteVersion,
			},
		})
	}
	return items
}

func fetchRemoteVersion(ctx context.Context, client *http.Client, url string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	info, ok := parsePLG(body)
	if !ok {
		return "", false
	}
	return info.Version, true
}
