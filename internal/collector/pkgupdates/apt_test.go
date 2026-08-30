package pkgupdates

import (
	"os"
	"path/filepath"
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
	listFile := filepath.Join(listsDir, "repo_Packages")
	writeFile(t, listFile, "Package: bash\nVersion: 2.0\n")

	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(listFile, old, old); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := aptItems(dpkgStatus, listsDir, time.Now())
	if len(items) != 1 {
		t.Fatalf("expected 1 outdated package, got %d", len(items))
	}
	ageStr := items[0].Attrs["cache_age_seconds"]
	if ageStr == "" {
		t.Fatal("expected cache_age_seconds to be set")
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
