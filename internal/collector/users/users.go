// Package users implements the system user inventory collector
// (ADR-0015): which accounts exist, and — best-effort, from Samba's own
// static configuration — which shares each can read or write. NFS has no
// per-user permission model to cross-reference (it's host-based, not
// user-based), so that half only ever comes from smb.conf.
package users

import (
	"bufio"
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

const (
	defaultPasswdFile = "/etc/passwd"
	defaultLoginDefs  = "/etc/login.defs"
	defaultSambaConf  = "/etc/samba/smb.conf"
	defaultUIDMin     = 1000
)

// Collector emits an Inventory of kind user (ADR-0015).
type Collector struct {
	passwdFile string
	loginDefs  string
	sambaConf  string
	hostID     string
}

// New returns a collector with production defaults.
func New() *Collector { return &Collector{} }

// Name implements collector.Collector.
func (c *Collector) Name() string { return "users" }

// Requires implements collector.Collector. /etc/passwd exists on every
// Linux host this project targets — no capability gate needed.
func (c *Collector) Requires() []collector.Capability { return nil }

// Init implements collector.Collector.
func (c *Collector) Init(ctx context.Context, cfg collector.Config, host *collector.HostInfo) error {
	c.passwdFile = configuredPath(cfg, "passwd_file", defaultPasswdFile)
	c.loginDefs = configuredPath(cfg, "login_defs", defaultLoginDefs)
	c.sambaConf = configuredPath(cfg, "samba_conf", defaultSambaConf)
	if host != nil {
		c.hostID = host.ID
	}
	return nil
}

// Collect implements collector.Collector.
func (c *Collector) Collect(ctx context.Context, sink collector.Sink) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	accounts, err := parsePasswd(c.passwdFile, uidMin(c.loginDefs))
	if err != nil {
		accounts = nil // no /etc/passwd readable — still emit an empty, valid snapshot
	}

	perms, _ := parseSambaPermissions(c.sambaConf) // best-effort; ok to be empty

	items := make([]schema.InventoryItem, 0, len(accounts))
	for _, a := range accounts {
		attrs := schema.Labels{"uid": strconv.Itoa(a.uid)}
		if p, ok := perms[a.name]; ok {
			if len(p.write) > 0 {
				attrs["shares_rw"] = strings.Join(p.write, ",")
			}
			if len(p.read) > 0 {
				attrs["shares_ro"] = strings.Join(p.read, ",")
			}
		}
		items = append(items, schema.InventoryItem{ID: a.name, Name: a.name, Attrs: attrs})
	}

	sink.Inventory(schema.Inventory{
		HostID:     c.hostID,
		Kind:       schema.InventoryUser,
		ReportedAt: time.Now().UTC(),
		Schema:     schema.CurrentSchemaVersion,
		Items:      items,
	})
	return nil
}

// Close implements collector.Collector.
func (c *Collector) Close() error { return nil }

func configuredPath(cfg collector.Config, key, fallback string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

type account struct {
	name string
	uid  int
}

// parsePasswd reads /etc/passwd, keeping only "real" accounts — those at
// or above minUID — and excluding the conventional nobody UID (65534),
// which login.defs' UID_MIN doesn't exclude on its own.
func parsePasswd(path string, minUID int) ([]account, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var accounts []account
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid < minUID || uid == 65534 {
			continue
		}
		accounts = append(accounts, account{name: fields[0], uid: uid})
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].name < accounts[j].name })
	return accounts, scanner.Err()
}

// uidMin reads UID_MIN from login.defs, falling back to the conventional
// default (1000, matching every distribution this project targets) when
// the file is missing or the directive isn't set.
func uidMin(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return defaultUIDMin
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "UID_MIN") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			return n
		}
	}
	return defaultUIDMin
}

type sharePerms struct {
	read  []string
	write []string
}

// parseSambaPermissions reads smb.conf's per-share "valid users" and
// "write list" directives (a simplified, best-effort read of Samba's
// permission model — it doesn't resolve groups, "invalid users",
// "admin users", or force-user semantics).
func parseSambaPermissions(path string) (map[string]sharePerms, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	perms := map[string]sharePerms{}
	var section string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section == "" || section == "global" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		users := strings.Fields(strings.TrimSpace(value))

		switch key {
		case "valid users":
			for _, u := range users {
				perms[u] = addRead(perms[u], section)
			}
		case "write list":
			for _, u := range users {
				perms[u] = addWrite(perms[u], section)
			}
		}
	}
	return perms, scanner.Err()
}

func addRead(p sharePerms, share string) sharePerms {
	p.read = append(p.read, share)
	return p
}

func addWrite(p sharePerms, share string) sharePerms {
	p.write = append(p.write, share)
	return p
}
