package schema

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// DefaultHostIDPath is where the agent persists its host_id in production
// (ADR-0004). Tests and tools should pass their own path instead of relying
// on this constant, so LoadOrCreateHostID stays testable.
const DefaultHostIDPath = "/var/lib/bitacora/host_id"

// LoadOrCreateHostID returns the host_id persisted at path, generating and
// persisting a new ULID if the file doesn't exist yet.
//
// host_id is deliberately NOT derived from hostname, MAC address or
// machine-id: all three change or get cloned across VM duplication, and
// host_id is the one identity that must survive that (ADR-0004).
func LoadOrCreateHostID(path string) (string, error) {
	existing, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(existing))
		if id == "" {
			return "", fmt.Errorf("host_id file %s is empty", path)
		}
		return id, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading host_id file %s: %w", path, err)
	}

	id, err := newULID()
	if err != nil {
		return "", fmt.Errorf("generating host_id: %w", err)
	}

	if err := persistHostID(path, id); err != nil {
		return "", err
	}

	return id, nil
}

func newULID() (string, error) {
	entropy := ulid.Monotonic(rand.Reader, 0)
	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// persistHostID writes id to path atomically: a temp file in the same
// directory, fsynced, then renamed over the destination. A partial write
// from a crash mid-save must never leave a corrupt or empty host_id.
func persistHostID(path, id string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".host_id-*")
	if err != nil {
		return fmt.Errorf("creating temp host_id file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(id + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing host_id: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsyncing host_id: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing host_id temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming host_id into place: %w", err)
	}
	return nil
}
