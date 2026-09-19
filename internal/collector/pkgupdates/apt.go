package pkgupdates

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/debversion"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// aptItems compares each installed package's version (/var/lib/dpkg/status)
// against the highest candidate version available in apt's own local
// metadata cache (/var/lib/apt/lists/*_Packages) — the same cache `apt
// update` maintains, read directly rather than via `exec` (ADR-0012,
// ADR-0017). Comparison uses dpkg's own version semantics
// (internal/debversion), never a plain string comparison.
func aptItems(dpkgStatus, listsDir string, now time.Time) []schema.InventoryItem {
	return aptItemsForSources(dpkgStatus, listsDir, "", "", now)
}

// aptItemsForSources is aptItems with optional source configuration paths.
// When no readable source configuration is supplied it keeps aptItems' legacy
// behavior, which is useful for hosts and tests that expose only the cache.
func aptItemsForSources(dpkgStatus, listsDir, sourcesList, sourcesDir string, now time.Time) []schema.InventoryItem {
	installed, err := parseDpkgStatus(dpkgStatus)
	if err != nil {
		return nil
	}

	prefixes, filterLists := activeAptListPrefixes(sourcesList, sourcesDir)
	candidates, cacheAge, err := candidateVersionsForSources(listsDir, prefixes, filterLists)
	if err != nil || len(candidates) == 0 {
		// No usable cache — `apt update` has never run on this host, or
		// the lists directory doesn't exist. Nothing to compare against,
		// not an error.
		return nil
	}

	items := make([]schema.InventoryItem, 0, len(installed))
	for name, installedVersion := range installed {
		candidate, ok := candidates[name]
		if !ok || debversion.Compare(candidate, installedVersion) <= 0 {
			continue
		}

		attrs := schema.Labels{
			"source":            "apt",
			"installed_version": installedVersion,
			"candidate_version": candidate,
		}
		if !cacheAge.IsZero() {
			attrs["cache_age_seconds"] = strconv.FormatFloat(now.Sub(cacheAge).Seconds(), 'f', 0, 64)
		}
		items = append(items, schema.InventoryItem{
			ID:    "apt:" + name,
			Name:  name,
			Attrs: attrs,
		})
	}
	return items
}

// parseDpkgStatus reads dpkg's own package database: one deb822 stanza
// per package, separated by a blank line. Only "Status: install ok
// installed" packages count — dpkg also lists packages that were removed
// but not purged (config files remain), which have no meaningful
// "installed version" to compare.
func parseDpkgStatus(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	installed := map[string]string{}
	var name, version, status string
	flush := func() {
		if name != "" && version != "" && strings.Contains(status, "installed") {
			installed[name] = version
		}
		name, version, status = "", "", ""
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // Description fields can be long
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // continuation of the previous field
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Package":
			name = strings.TrimSpace(value)
		case "Version":
			version = strings.TrimSpace(value)
		case "Status":
			status = strings.TrimSpace(value)
		}
	}
	flush()
	return installed, scanner.Err()
}

// candidateVersions reads every configured repository's local package
// list, keeping the highest version found per package name — a package
// can appear in more than one enabled repo (e.g. both a distro's main
// archive and its security updates) at different versions. The returned
// time is the OLDEST mtime among the *_Packages files that contributed:
// apt's own notion of "how stale is my worst source", not the newest.
func candidateVersions(listsDir string) (map[string]string, time.Time, error) {
	return candidateVersionsForSources(listsDir, nil, false)
}

// candidateVersionsForSources reads only list files backed by currently
// configured binary-package sources. apt leaves old list files behind when a
// source is removed; treating those as current makes both candidates and cache
// age lie. If source configuration is unavailable, callers may deliberately
// retain the old cache-only behavior by passing filterLists=false.
func candidateVersionsForSources(listsDir string, prefixes map[string]struct{}, filterLists bool) (map[string]string, time.Time, error) {
	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return nil, time.Time{}, err
	}

	candidates := map[string]string{}
	var oldest time.Time
	var any bool

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_Packages") || (filterLists && !matchesAptSource(e.Name(), prefixes)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		pkgs, err := parsePackagesFile(filepath.Join(listsDir, e.Name()))
		if err != nil {
			continue
		}
		if !any || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
		}
		any = true
		for name, version := range pkgs {
			if current, ok := candidates[name]; !ok || debversion.Compare(version, current) > 0 {
				candidates[name] = version
			}
		}
	}
	if !any {
		return candidates, time.Time{}, nil
	}
	return candidates, oldest, nil
}

func matchesAptSource(listName string, prefixes map[string]struct{}) bool {
	for prefix := range prefixes {
		if strings.HasPrefix(listName, prefix) {
			return true
		}
	}
	return false
}

// activeAptListPrefixes returns the filenames apt uses for active binary
// package sources. The boolean means source configuration was readable: an
// empty but readable configuration intentionally matches no cached lists.
func activeAptListPrefixes(sourcesList, sourcesDir string) (map[string]struct{}, bool) {
	prefixes := map[string]struct{}{}
	configured := false
	parse := func(path string, contents []byte) {
		configured = true
		if strings.HasSuffix(path, ".sources") {
			for prefix := range parseDeb822AptSources(string(contents)) {
				prefixes[prefix] = struct{}{}
			}
			return
		}
		for prefix := range parseLegacyAptSources(string(contents)) {
			prefixes[prefix] = struct{}{}
		}
	}
	if sourcesList != "" {
		if contents, err := os.ReadFile(sourcesList); err == nil {
			parse(sourcesList, contents)
		}
	}
	if sourcesDir == "" {
		return prefixes, configured
	}
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return prefixes, configured
	}
	configured = true
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".list") && !strings.HasSuffix(entry.Name(), ".sources")) {
			continue
		}
		path := filepath.Join(sourcesDir, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parse(path, contents)
	}
	return prefixes, configured
}

func parseLegacyAptSources(contents string) map[string]struct{} {
	prefixes := map[string]struct{}{}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "deb" {
			continue
		}
		index := 1
		if index < len(fields) && strings.HasPrefix(fields[index], "[") {
			for index < len(fields) && !strings.HasSuffix(fields[index], "]") {
				index++
			}
			index++
		}
		if len(fields) < index+3 {
			continue
		}
		uri, suite := fields[index], fields[index+1]
		for _, component := range fields[index+2:] {
			if prefix, ok := aptListPrefix(uri, suite, component); ok {
				prefixes[prefix] = struct{}{}
			}
		}
	}
	return prefixes
}

func parseDeb822AptSources(contents string) map[string]struct{} {
	prefixes := map[string]struct{}{}
	fields := map[string]string{}
	flush := func() {
		types := strings.Fields(fields["types"])
		if strings.EqualFold(fields["enabled"], "no") || !contains(types, "deb") {
			fields = map[string]string{}
			return
		}
		for _, uri := range strings.Fields(fields["uris"]) {
			for _, suite := range strings.Fields(fields["suites"]) {
				for _, component := range strings.Fields(fields["components"]) {
					if prefix, ok := aptListPrefix(uri, suite, component); ok {
						prefixes[prefix] = struct{}{}
					}
				}
			}
		}
		fields = map[string]string{}
	}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	flush()
	return prefixes
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func aptListPrefix(rawURI, suite, component string) (string, bool) {
	uri, err := url.Parse(rawURI)
	if err != nil || uri.Host == "" || suite == "" || component == "" {
		return "", false
	}
	base := uri.Host + "_" + strings.ReplaceAll(strings.Trim(uri.Path, "/"), "/", "_")
	base = strings.TrimSuffix(base, "_")
	return base + "_dists_" + suite + "_" + component + "_", true
}

// parsePackagesFile reads one apt list cache file — the same deb822
// stanza format as dpkg's status file, just without a Status field.
func parsePackagesFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pkgs := map[string]string{}
	var name, version string
	flush := func() {
		if name != "" && version != "" {
			pkgs[name] = version
		}
		name, version = "", ""
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Package":
			name = strings.TrimSpace(value)
		case "Version":
			version = strings.TrimSpace(value)
		}
	}
	flush()
	return pkgs, scanner.Err()
}
