package pkgupdates

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// realisticEntityBasedPLG mirrors the real UnRaid plugin format, which
// declares its attributes as DOCTYPE entities and references them via
// &name; in the root tag — the format encoding/xml can't resolve on its
// own (it doesn't process DTD-internal entity definitions).
const realisticEntityBasedPLG = `<?xml version="1.0" standalone="yes"?>
<!DOCTYPE PLUGIN [
<!ENTITY name      "my.plugin">
<!ENTITY version   "2024.01.15">
<!ENTITY pluginURL "%s">
]>
<PLUGIN name="&name;" version="&version;" pluginURL="&pluginURL;">
<CHANGES>
some changelog text
</CHANGES>
</PLUGIN>
`

const literalAttributePLG = `<?xml version="1.0" standalone="yes"?>
<PLUGIN name="simple.plugin" version="1.2.3" pluginURL="%s">
</PLUGIN>
`

func TestParsePLG_ResolvesEntityDeclaredAttributes(t *testing.T) {
	data := []byte(fmt.Sprintf(realisticEntityBasedPLG, "https://example.invalid/my.plugin.plg"))
	info, ok := parsePLG(data)
	if !ok {
		t.Fatal("expected the entity-based .plg to parse")
	}
	if info.Name != "my.plugin" || info.Version != "2024.01.15" {
		t.Fatalf("unexpected parsed info: %+v", info)
	}
	if info.PluginURL != "https://example.invalid/my.plugin.plg" {
		t.Fatalf("unexpected pluginURL: %q", info.PluginURL)
	}
}

func TestParsePLG_ResolvesLiteralAttributes(t *testing.T) {
	data := []byte(fmt.Sprintf(literalAttributePLG, "https://example.invalid/simple.plugin.plg"))
	info, ok := parsePLG(data)
	if !ok {
		t.Fatal("expected the literal-attribute .plg to parse")
	}
	if info.Name != "simple.plugin" || info.Version != "1.2.3" {
		t.Fatalf("unexpected parsed info: %+v", info)
	}
}

func TestParsePLG_MissingRequiredAttributesFails(t *testing.T) {
	if _, ok := parsePLG([]byte(`<PLUGIN pluginURL="https://x">`)); ok {
		t.Fatal("expected a .plg with no name/version to fail to parse")
	}
}

func TestUnraidItems_DetectsNewerRemoteVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(realisticEntityBasedPLG, "ignored-in-remote-copy")))
		// The remote copy declares a newer version than the local one.
	}))
	defer server.Close()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "my.plugin.plg"), fmt.Sprintf(
		`<?xml version="1.0" standalone="yes"?>
<!DOCTYPE PLUGIN [
<!ENTITY name      "my.plugin">
<!ENTITY version   "2023.01.01">
<!ENTITY pluginURL "%s">
]>
<PLUGIN name="&name;" version="&version;" pluginURL="&pluginURL;">
</PLUGIN>
`, server.URL))

	items := unraidItems(context.Background(), dir, server.Client())
	if len(items) != 1 {
		t.Fatalf("expected 1 outdated plugin, got %d: %+v", len(items), items)
	}
	if items[0].Attrs["installed_version"] != "2023.01.01" {
		t.Fatalf("unexpected installed_version: %+v", items[0].Attrs)
	}
	if items[0].Attrs["candidate_version"] != "2024.01.15" {
		t.Fatalf("unexpected candidate_version: %+v", items[0].Attrs)
	}
}

func TestUnraidItems_SameVersionIsNotOutdated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf(literalAttributePLG, "unused")))
	}))
	defer server.Close()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simple.plugin.plg"), fmt.Sprintf(literalAttributePLG, server.URL))

	items := unraidItems(context.Background(), dir, server.Client())
	if len(items) != 0 {
		t.Fatalf("expected no outdated plugins when versions match, got %+v", items)
	}
}

func TestUnraidItems_UnreachableSourceDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "my.plugin.plg"), fmt.Sprintf(literalAttributePLG, "http://127.0.0.1:1"))

	items := unraidItems(context.Background(), dir, http.DefaultClient)
	if len(items) != 0 {
		t.Fatalf("expected an unreachable plugin source to degrade to no item, not an error, got %+v", items)
	}
}

func TestUnraidItems_NoPluginsDirYieldsNoItemsNotError(t *testing.T) {
	items := unraidItems(context.Background(), filepath.Join(t.TempDir(), "no-plugins"), http.DefaultClient)
	if items != nil {
		t.Fatalf("expected nil items on a non-UnRaid host, got %+v", items)
	}
}
