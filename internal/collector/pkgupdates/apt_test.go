package pkgupdates

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

const dpkgStatusFixture = `Package: bash
Status: install ok installed
Priority: required
Version: 5.1-6ubuntu1
Description: the GNU Bourne Again shell

Package: curl
Status: install ok installed
Version: 7.81.0-1ubuntu1.10
Description: command line tool for transferring data

Package: removed-pkg
Status: deinstall ok config-files
Version: 1.0-1
Description: no longer installed, config remains
`

const aptListsFixtureA = `Package: bash
Version: 5.1-6ubuntu1.1
Priority: required

Package: curl
Version: 7.81.0-1ubuntu1.9
`

// A second repo file offering a higher curl version than the first —
// exercises "take the highest candidate across every enabled repo".
const aptListsFixtureB = `Package: curl
Version: 7.81.0-1ubuntu1.16
`

func TestAptItems_DetectsOutdatedPackages(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	writeFile(t, dpkgStatus, dpkgStatusFixture)
	writeFile(t, filepath.Join(listsDir, "archive.ubuntu.com_ubuntu_dists_jammy_main_binary-amd64_Packages"), aptListsFixtureA)
	writeFile(t, filepath.Join(listsDir, "security.ubuntu.com_ubuntu_dists_jammy-security_main_binary-amd64_Packages"), aptListsFixtureB)

	items := aptItems(dpkgStatus, listsDir, time.Now())
	if len(items) != 2 {
		t.Fatalf("expected 2 outdated packages (bash, curl), got %d: %+v", len(items), items)
	}

	byName := map[string]map[string]string{}
	for _, it := range items {
		byName[it.Name] = it.Attrs
	}

	if got := byName["bash"]["candidate_version"]; got != "5.1-6ubuntu1.1" {
		t.Fatalf("expected bash candidate 5.1-6ubuntu1.1, got %q", got)
	}
	// curl's candidate must be the HIGHEST across both repo files, not
	// whichever file happened to be read last.
	if got := byName["curl"]["candidate_version"]; got != "7.81.0-1ubuntu1.16" {
		t.Fatalf("expected curl candidate to be the highest across repos (7.81.0-1ubuntu1.16), got %q", got)
	}
	if _, ok := byName["removed-pkg"]; ok {
		t.Fatal("expected a deinstalled package (config-files only) to be excluded")
	}
}

func TestAptItems_SameVersionIsNotOutdated(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 5.1-6ubuntu1\n\n")
	writeFile(t, filepath.Join(listsDir, "repo_Packages"), "Package: bash\nVersion: 5.1-6ubuntu1\n")

	items := aptItems(dpkgStatus, listsDir, time.Now())
	if len(items) != 0 {
		t.Fatalf("expected no outdated packages, got %+v", items)
	}
}

func TestAptItems_UsesNumericNotLexicalVersionOrdering(t *testing.T) {
	// A naive string comparison would treat "1.9" as newer than "1.10" —
	// this must not flag the package as outdated.
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	writeFile(t, dpkgStatus, "Package: foo\nStatus: install ok installed\nVersion: 1.10\n\n")
	writeFile(t, filepath.Join(listsDir, "repo_Packages"), "Package: foo\nVersion: 1.9\n")

	items := aptItems(dpkgStatus, listsDir, time.Now())
	if len(items) != 0 {
		t.Fatalf("expected 1.10 (installed) not to be considered older than 1.9 (candidate), got %+v", items)
	}
}

func TestAptItems_ReportsCacheAge(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 1.0\n\n")
	writeFile(t, filepath.Join(listsDir, "repo_Packages"), "Package: bash\nVersion: 2.0\n")

	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(listsDir, old, old); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := aptItems(dpkgStatus, listsDir, time.Now())
	if len(items) != 1 {
		t.Fatalf("expected 1 outdated package, got %d", len(items))
	}
	age, err := strconv.ParseFloat(items[0].Attrs["cache_age_seconds"], 64)
	if err != nil {
		t.Fatalf("parsing cache age: %v", err)
	}
	if age < 47*60*60 || age > 49*60*60 {
		t.Fatalf("expected a cache age near 48 hours, got %.0f seconds", age)
	}
}

// Regression for icloudserver on 2026-10-08: the panel read "package cache
// outdated: 896.8 days" hours after a successful `apt update`, because the age
// came from the oldest *_Packages mtime and apt preserves each index's remote
// Last-Modified. Ubuntu's release pocket never republishes, so
// archive.ubuntu.com_ubuntu_dists_noble_main_binary-amd64_Packages still
// carried noble's release date. The frozen index below is set 897 days old on
// purpose: it must not contribute to the age at all.
func TestAptItemsForSources_CacheAgeTracksLastUpdateNotFrozenIndexMtime(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	sourcesDir := filepath.Join(dir, "sources.list.d")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 1.0\n\n")
	writeFile(t, filepath.Join(sourcesDir, "ubuntu.sources"), "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu/\nSuites: noble noble-updates\nComponents: main\n\n")
	frozen := filepath.Join(listsDir, "archive.ubuntu.com_ubuntu_dists_noble_main_binary-amd64_Packages")
	updates := filepath.Join(listsDir, "archive.ubuntu.com_ubuntu_dists_noble-updates_main_binary-amd64_Packages")
	writeFile(t, frozen, "Package: bash\nVersion: 1.0\n")
	writeFile(t, updates, "Package: bash\nVersion: 2.0\n")
	stamp := filepath.Join(dir, "periodic", "update-success-stamp")
	writeFile(t, stamp, "")

	now := time.Now()
	refreshed := now.Add(-2 * time.Hour)
	published := now.Add(-30 * 24 * time.Hour)
	for path, timestamp := range map[string]time.Time{
		frozen:   now.Add(-897 * 24 * time.Hour),
		updates:  published,
		stamp:    refreshed,
		listsDir: refreshed,
	} {
		if err := os.Chtimes(path, timestamp, timestamp); err != nil {
			t.Fatalf("setting time for %s: %v", path, err)
		}
	}

	items := aptItemsForSources(dpkgStatus, listsDir, filepath.Join(dir, "sources.list"), sourcesDir, now)
	if len(items) != 1 || items[0].Attrs["candidate_version"] != "2.0" {
		t.Fatalf("expected one candidate from noble-updates, got %+v", items)
	}
	age, err := strconv.ParseFloat(items[0].Attrs["cache_age_seconds"], 64)
	if err != nil {
		t.Fatalf("parsing cache age: %v", err)
	}
	if age > 24*60*60 {
		t.Fatalf("a frozen index made a cache refreshed 2 hours ago look stale: got %.0f seconds", age)
	}
	if age < 60*60 {
		t.Fatalf("expected the age of the last update (about 2 hours), got %.0f seconds", age)
	}
}

func TestAptCacheRefreshedAt_PrefersTheNewestUpdateSignal(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	listsDir := filepath.Join(dir, "lists")
	partialDir := filepath.Join(listsDir, "partial")
	stamp := filepath.Join(dir, "periodic", "update-success-stamp")
	writeFile(t, filepath.Join(listsDir, "repo_Packages"), "Package: bash\nVersion: 2.0\n")
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writeFile(t, stamp, "")

	for _, tc := range []struct {
		name  string
		times map[string]time.Time
		want  time.Time
	}{
		{
			name:  "post-invoke stamp is the newest",
			times: map[string]time.Time{stamp: now.Add(-time.Minute), listsDir: now.Add(-72 * time.Hour), partialDir: now.Add(-72 * time.Hour)},
			want:  now.Add(-time.Minute),
		},
		{
			name:  "a replaced index is newer than the last recorded success",
			times: map[string]time.Time{stamp: now.Add(-72 * time.Hour), listsDir: now.Add(-time.Minute), partialDir: now.Add(-72 * time.Hour)},
			want:  now.Add(-time.Minute),
		},
		{
			name:  "partial downloads are newer than both",
			times: map[string]time.Time{stamp: now.Add(-72 * time.Hour), listsDir: now.Add(-72 * time.Hour), partialDir: now.Add(-time.Minute)},
			want:  now.Add(-time.Minute),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for path, timestamp := range tc.times {
				if err := os.Chtimes(path, timestamp, timestamp); err != nil {
					t.Fatalf("setting time for %s: %v", path, err)
				}
			}
			got := aptCacheRefreshedAt(listsDir)
			if diff := got.Sub(tc.want); diff > time.Second || diff < -time.Second {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// update-success-stamp belongs to update-notifier-common, not to apt, so a
// Debian host without that package has no stamp at all. The lists directory
// still moves whenever apt replaces an index, and that is what must be used —
// not the frozen index mtimes, and not nothing.
func TestAptCacheRefreshedAt_FallsBackToListsDirWithoutUpdateStamp(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	listsDir := filepath.Join(dir, "lists")
	frozen := filepath.Join(listsDir, "repo_Packages")
	writeFile(t, frozen, "Package: bash\nVersion: 2.0\n")

	refreshed := now.Add(-6 * time.Hour)
	if err := os.Chtimes(frozen, now.Add(-897*24*time.Hour), now.Add(-897*24*time.Hour)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := os.Chtimes(listsDir, refreshed, refreshed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := aptCacheRefreshedAt(listsDir)
	if diff := got.Sub(refreshed); diff > time.Second || diff < -time.Second {
		t.Fatalf("got %s, want the lists directory time %s", got, refreshed)
	}
}

func TestAptCacheRefreshedAt_MissingListsDirHasNoSignal(t *testing.T) {
	if got := aptCacheRefreshedAt(filepath.Join(t.TempDir(), "lists")); !got.IsZero() {
		t.Fatalf("expected no signal on a host where apt never ran, got %s", got)
	}
}

func TestAptItemsForSources_IgnoresOldOrphanedListAlongsideFreshActiveList(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	sourcesList := filepath.Join(dir, "sources.list")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 1.0\n\n")
	writeFile(t, sourcesList, "deb http://active.example/apt stable main\n")
	active := filepath.Join(listsDir, "active.example_apt_dists_stable_main_binary-amd64_Packages")
	writeFile(t, active, "Package: bash\nVersion: 2.0\n")
	orphan := filepath.Join(listsDir, "old.example_apt_dists_stable_main_binary-amd64_Packages")
	writeFile(t, orphan, "Package: bash\nVersion: 99.0\n")

	now := time.Now()
	fresh := now.Add(-time.Hour)
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(active, fresh, fresh); err != nil {
		t.Fatalf("setting active list time: %v", err)
	}
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("setting orphan list time: %v", err)
	}

	items := aptItemsForSources(dpkgStatus, listsDir, sourcesList, filepath.Join(dir, "sources.list.d"), now)
	if len(items) != 1 {
		t.Fatalf("expected one active-source update, got %+v", items)
	}
	if got := items[0].Attrs["candidate_version"]; got != "2.0" {
		t.Fatalf("candidate included orphaned list: got %q, want 2.0", got)
	}
	age, err := strconv.ParseFloat(items[0].Attrs["cache_age_seconds"], 64)
	if err != nil {
		t.Fatalf("parsing cache age: %v", err)
	}
	if age > 2*60*60 {
		t.Fatalf("orphaned list made active cache stale: got %.0f seconds", age)
	}
}

func TestAptItemsForSources_IgnoresDisabledSourceCandidates(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	listsDir := filepath.Join(dir, "lists")
	sourcesList := filepath.Join(dir, "sources.list")
	sourcesDir := filepath.Join(dir, "sources.list.d")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 1.0\n\n")
	writeFile(t, sourcesList, "# a second source is intentionally still enabled\ndeb http://fresh.example/apt stable main\n")
	writeFile(t, filepath.Join(sourcesDir, "other.sources"), "Types: deb deb-src\nURIs: http://other.example/apt\nSuites: stable\nComponents: main\nEnabled: yes\n\n")
	writeFile(t, filepath.Join(sourcesDir, "disabled.sources"), "Types: deb\nURIs: http://disabled.example/apt\nSuites: stable\nComponents: main\nEnabled: no\n\n")
	writeFile(t, filepath.Join(listsDir, "fresh.example_apt_dists_stable_main_binary-amd64_Packages"), "Package: bash\nVersion: 2.0\n")
	writeFile(t, filepath.Join(listsDir, "other.example_apt_dists_stable_main_binary-amd64_Packages"), "Package: ignored-package\nVersion: 2.0\n")
	writeFile(t, filepath.Join(listsDir, "disabled.example_apt_dists_stable_main_binary-amd64_Packages"), "Package: bash\nVersion: 99.0\n")

	items := aptItemsForSources(dpkgStatus, listsDir, sourcesList, sourcesDir, time.Now())
	if len(items) != 1 || items[0].Attrs["candidate_version"] != "2.0" {
		t.Fatalf("expected only the enabled source's candidate, got %+v", items)
	}
}

func TestAptItems_MissingListsDirYieldsNoItemsNotError(t *testing.T) {
	dir := t.TempDir()
	dpkgStatus := filepath.Join(dir, "status")
	writeFile(t, dpkgStatus, "Package: bash\nStatus: install ok installed\nVersion: 1.0\n\n")

	items := aptItems(dpkgStatus, filepath.Join(dir, "no-lists"), time.Now())
	if items != nil {
		t.Fatalf("expected nil items when apt has never updated its cache, got %+v", items)
	}
}

func TestAptItems_MissingDpkgStatusYieldsNoItemsNotError(t *testing.T) {
	dir := t.TempDir()
	items := aptItems(filepath.Join(dir, "no-status"), filepath.Join(dir, "no-lists"), time.Now())
	if items != nil {
		t.Fatalf("expected nil items on a non-dpkg host, got %+v", items)
	}
}
