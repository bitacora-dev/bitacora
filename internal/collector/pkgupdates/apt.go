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

	sources, filterLists := activeAptSources(sourcesList, sourcesDir)
	candidates, err := candidateVersionsForSources(listsDir, sources, filterLists)
	if err != nil || len(candidates) == 0 {
		// No usable cache — `apt update` has never run on this host, or
		// the lists directory doesn't exist. Nothing to compare against,
		// not an error.
		return nil
	}

	refreshedAt := aptCacheRefreshedAt(listsDir)
	suites := newAptSuitePolicy(listsDir)

	items := make([]schema.InventoryItem, 0, len(installed))
	for name, installedVersion := range installed {
		candidate, ok := candidates[name]
		if !ok || debversion.Compare(candidate.version, installedVersion) <= 0 {
			continue
		}

		attrs := schema.Labels{
			"source":            "apt",
			"installed_version": installedVersion,
			"candidate_version": candidate.version,
		}
		if !refreshedAt.IsZero() {
			attrs["cache_age_seconds"] = strconv.FormatFloat(now.Sub(refreshedAt).Seconds(), 'f', 0, 64)
		}
		if candidate.source.suite != "" {
			attrs["candidate_suite"] = candidate.source.suite
			attrs["candidate_automatic"] = strconv.FormatBool(suites.automatic(candidate.source))
		}
		items = append(items, schema.InventoryItem{
			ID:    "apt:" + name,
			Name:  name,
			Attrs: attrs,
		})
	}
	return items
}

// aptCacheRefreshedAt reports when apt last refreshed its package lists.
//
// It deliberately does NOT look at the mtime of the *_Packages files. apt
// preserves each index's remote Last-Modified timestamp on the local copy, so
// that mtime says when the repository last published that index, not when
// this host last fetched it. Ubuntu's release pocket never republishes:
// `archive.ubuntu.com_ubuntu_dists_noble_main_binary-amd64_Packages` carries
// noble's release date forever. Deriving the cache age from the oldest of
// those files is what made icloudserver report "package cache outdated:
// 896.8 days" on 2026-10-08, hours after a successful `apt update` — the
// number was the age of noble's frozen index, not of the cache.
//
// Three signals are read instead, all pure reads (ADR-0012), newest wins
// because each one only moves forward when apt itself does work:
//
//   - <state>/periodic/update-success-stamp, touched by
//     APT::Update::Post-Invoke-Success (/etc/apt/apt.conf.d/15update-stamp,
//     shipped by update-notifier-common), so it marks the end of a
//     successful `apt update` exactly. Absent when that package is not
//     installed, which is why it is not the only signal.
//   - the lists directory itself, and its partial/ subdirectory: apt
//     downloads into partial/ and renames the result into lists/, so both
//     move whenever an index is actually replaced.
//
// The remaining blind spot is honest and bounded: on a host without
// update-notifier-common whose every configured suite is frozen, no index is
// ever replaced and the age keeps growing. That reports the cache as old,
// which is the safe direction — it never claims a stale cache is fresh.
func aptCacheRefreshedAt(listsDir string) time.Time {
	var newest time.Time
	consider := func(path string) {
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	// apt's own layout: Dir::State is the parent of Dir::State::lists.
	consider(filepath.Join(filepath.Dir(listsDir), "periodic", "update-success-stamp"))
	consider(listsDir)
	consider(filepath.Join(listsDir, "partial"))
	return newest
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

// aptCandidate is the highest version found for one package across every
// readable list file, together with the configured source that carried it —
// a package can appear in more than one enabled repo (e.g. both a distro's
// main archive and its backports suite) at different versions.
type aptCandidate struct {
	version string
	source  aptSource
}

// candidateVersionsForSources reads only list files backed by currently
// configured binary-package sources. apt leaves old list files behind when a
// source is removed; treating those as current makes the candidates lie. If
// source configuration is unavailable, callers may deliberately retain the
// old cache-only behavior by passing filterLists=false — the candidate then
// carries no source, and the origin attributes are omitted rather than
// guessed from the file name.
func candidateVersionsForSources(listsDir string, sources map[string]aptSource, filterLists bool) (map[string]aptCandidate, error) {
	entries, err := os.ReadDir(listsDir)
	if err != nil {
		return nil, err
	}

	candidates := map[string]aptCandidate{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_Packages") {
			continue
		}
		source, matched := matchingAptSource(e.Name(), sources)
		if filterLists && !matched {
			continue
		}

		pkgs, err := parsePackagesFile(filepath.Join(listsDir, e.Name()))
		if err != nil {
			continue
		}
		for name, version := range pkgs {
			if current, ok := candidates[name]; !ok || debversion.Compare(version, current.version) > 0 {
				candidates[name] = aptCandidate{version: version, source: source}
			}
		}
	}
	return candidates, nil
}

// aptSource is one configured binary-package source, named the way apt names
// the files it writes for it under the lists directory.
type aptSource struct {
	listPrefix    string // host_path_dists_suite_component_
	releasePrefix string // host_path_dists_suite_
	suite         string
}

func matchingAptSource(listName string, sources map[string]aptSource) (aptSource, bool) {
	for prefix, source := range sources {
		if strings.HasPrefix(listName, prefix) {
			return source, true
		}
	}
	return aptSource{}, false
}

// aptSuitePolicy answers, per suite, whether apt installs versions from it on
// its own initiative.
//
// Ubuntu's backports suite declares `NotAutomatic: yes` in its Release index,
// which pins every version it carries below the release pocket: `apt upgrade`
// leaves them alone, and `apt-cache policy` reports the installed version as
// the candidate even though a higher one is cached. (`ButAutomaticUpgrades:
// yes`, which Ubuntu also sets, only re-enables upgrades for packages whose
// installed version already came from that suite — which is not knowable from
// dpkg's database, so it is not claimed here.) The newer version is really
// there, so it is still reported; what the suite attributes add is where it
// comes from and that apt will not take it by itself, instead of presenting
// an unactionable version jump as a pending update.
type aptSuitePolicy struct {
	listsDir string
	known    map[string]bool
}

func newAptSuitePolicy(listsDir string) *aptSuitePolicy {
	return &aptSuitePolicy{listsDir: listsDir, known: map[string]bool{}}
}

func (p *aptSuitePolicy) automatic(source aptSource) bool {
	if source.releasePrefix == "" {
		return true
	}
	if automatic, ok := p.known[source.releasePrefix]; ok {
		return automatic
	}
	automatic := true
	// InRelease is the inline-signed form and Release the detached one; the
	// fields are plain text in both, so one line scan reads either.
	for _, name := range []string{source.releasePrefix + "InRelease", source.releasePrefix + "Release"} {
		contents, err := os.ReadFile(filepath.Join(p.listsDir, name))
		if err != nil {
			continue
		}
		automatic = !releaseDeclaresNotAutomatic(string(contents))
		break
	}
	p.known[source.releasePrefix] = automatic
	return automatic
}

func releaseDeclaresNotAutomatic(contents string) bool {
	for _, line := range strings.Split(contents, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "NotAutomatic") {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(value), "yes")
	}
	return false
}

// activeAptSources returns the currently configured binary-package sources,
// keyed by the list-file prefix apt derives from each one. The boolean means
// source configuration was readable: an empty but readable configuration
// intentionally matches no cached lists.
func activeAptSources(sourcesList, sourcesDir string) (map[string]aptSource, bool) {
	sources := map[string]aptSource{}
	configured := false
	parse := func(path string, contents []byte) {
		configured = true
		parsed := parseLegacyAptSources(string(contents))
		if strings.HasSuffix(path, ".sources") {
			parsed = parseDeb822AptSources(string(contents))
		}
		for prefix, source := range parsed {
			sources[prefix] = source
		}
	}
	if sourcesList != "" {
		if contents, err := os.ReadFile(sourcesList); err == nil {
			parse(sourcesList, contents)
		}
	}
	if sourcesDir == "" {
		return sources, configured
	}
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return sources, configured
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
	return sources, configured
}

func parseLegacyAptSources(contents string) map[string]aptSource {
	sources := map[string]aptSource{}
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
			if source, ok := aptSourceFor(uri, suite, component); ok {
				sources[source.listPrefix] = source
			}
		}
	}
	return sources
}

func parseDeb822AptSources(contents string) map[string]aptSource {
	sources := map[string]aptSource{}
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
					if source, ok := aptSourceFor(uri, suite, component); ok {
						sources[source.listPrefix] = source
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
	return sources
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func aptSourceFor(rawURI, suite, component string) (aptSource, bool) {
	uri, err := url.Parse(rawURI)
	if err != nil || uri.Host == "" || suite == "" || component == "" {
		return aptSource{}, false
	}
	base := uri.Host + "_" + strings.ReplaceAll(strings.Trim(uri.Path, "/"), "/", "_")
	base = strings.TrimSuffix(base, "_")
	release := base + "_dists_" + suite + "_"
	return aptSource{listPrefix: release + component + "_", releasePrefix: release, suite: suite}, true
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
