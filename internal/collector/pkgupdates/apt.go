package pkgupdates

import (
	"bufio"
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
	installed, err := parseDpkgStatus(dpkgStatus)
	if err != nil {
		return nil
	}

	candidates, cacheAge, err := candidateVersions(listsDir)
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
	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return nil, time.Time{}, err
	}

	candidates := map[string]string{}
	var oldest time.Time
	var any bool

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_Packages") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !any || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
		}
		any = true

		pkgs, err := parsePackagesFile(filepath.Join(listsDir, e.Name()))
		if err != nil {
			continue
		}
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
