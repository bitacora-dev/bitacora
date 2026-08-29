package capabilities

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// Config carries every path the detector looks at. Production code uses
// DefaultConfig; tests point Root (and, where a check needs it, an
// individual path) at a temporary directory instead of the real host, the
// same pattern internal/doctor uses.
type Config struct {
	// Root prefixes every absolute path below. "/" in production, a
	// t.TempDir() in tests.
	Root string

	SystemdDirs     []string
	JournaldDirs    []string
	SyslogFiles     []string
	DpkgStatus      string
	RPMDBDir        string
	SmartctlPaths   []string
	MdstatPath      string
	SnapraidConf    string
	ProcMounts      string
	UnraidMdcmd     string
	HwmonDir        string
	EdacDir         string
	RasdaemonPaths  []string
	PstoreDir       string
	DockerSocket    string
	CgroupV2Marker  string
	TailscaleSocket string
	SelinuxDir      string
	SelinuxEnforce  string
	ApparmorDir     string
	OSReleasePath   string
	KernelPath      string

	// PubliclyExposed is operator-declared, not detected: whether the host's
	// ingestion surface reaches the public internet can't be inferred from
	// the filesystem (ADR-0004: "Superficie: internet público" for the VPS).
	PubliclyExposed bool
}

// DefaultConfig is the real, production filesystem layout this project
// targets (ADR-0004).
var DefaultConfig = Config{
	Root:            "/",
	SystemdDirs:     []string{"/run/systemd/system"},
	JournaldDirs:    []string{"/run/systemd/journal", "/var/log/journal"},
	SyslogFiles:     []string{"/var/log/syslog", "/var/log/messages"},
	DpkgStatus:      "/var/lib/dpkg/status",
	RPMDBDir:        "/var/lib/rpm",
	SmartctlPaths:   []string{"/usr/sbin/smartctl", "/sbin/smartctl", "/usr/bin/smartctl"},
	MdstatPath:      "/proc/mdstat",
	SnapraidConf:    "/etc/snapraid.conf",
	ProcMounts:      "/proc/mounts",
	UnraidMdcmd:     "/proc/mdcmd",
	HwmonDir:        "/sys/class/hwmon",
	EdacDir:         "/sys/devices/system/edac",
	RasdaemonPaths:  []string{"/usr/sbin/rasdaemon", "/sbin/rasdaemon"},
	PstoreDir:       "/sys/fs/pstore",
	DockerSocket:    "/var/run/docker.sock",
	CgroupV2Marker:  "/sys/fs/cgroup/cgroup.controllers",
	TailscaleSocket: "/var/run/tailscale/tailscaled.sock",
	SelinuxDir:      "/sys/fs/selinux",
	SelinuxEnforce:  "/sys/fs/selinux/enforce",
	ApparmorDir:     "/sys/kernel/security/apparmor",
	OSReleasePath:   "/etc/os-release",
	KernelPath:      "/proc/sys/kernel/osrelease",
}

// degradedImpact documents, for capabilities whose absence has a known
// visible consequence, what the user loses and where to fix it (ADR-0004
// "degraded" list). Capabilities not listed here simply don't appear in
// Manifest.Degraded when unavailable — most gaps are self-explanatory (no
// dnf on a non-RPM host isn't a degradation, it's expected).
var degradedImpact = map[collector.Capability]Degraded{
	HwPstore: {
		Impact: "no se podrá diagnosticar un cuelgue duro",
		Remedy: "docs/setup/ramoops.md",
	},
	StorageSmart: {
		Impact: "no se podrán leer métricas SMART de los discos",
	},
	HwHwmon: {
		Impact: "no se podrán leer temperaturas ni sensores del hardware",
	},
}

// path joins root with an absolute-looking child path, the same way
// internal/doctor's tests substitute a temp directory for "/".
func (cfg Config) path(p string) string {
	if p == "" {
		return ""
	}
	if cfg.Root == "" || cfg.Root == "/" {
		return p
	}
	return filepath.Join(cfg.Root, p)
}

func (cfg Config) exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(cfg.path(p))
	return err == nil
}

func (cfg Config) anyExists(paths []string) (string, bool) {
	for _, p := range paths {
		if cfg.exists(p) {
			return p, true
		}
	}
	return "", false
}

func (cfg Config) dirNonEmpty(p string) bool {
	entries, err := os.ReadDir(cfg.path(p))
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// Detect probes the host described by cfg and builds its capability
// manifest. now and agentVersion are passed in explicitly so the result is
// deterministic and testable.
func Detect(cfg Config, hostID, hostname, agentVersion string, now time.Time) Manifest {
	m := Manifest{
		HostID:       hostID,
		Hostname:     hostname,
		ReportedAt:   now,
		AgentVersion: agentVersion,
		OS:           detectOS(cfg),
		Capabilities: make(map[collector.Capability]Status, len(All)),
	}

	for _, cap := range All {
		m.Capabilities[cap] = detectOne(cfg, cap)
	}

	for cap, status := range m.Capabilities {
		if status.Available {
			continue
		}
		if d, ok := degradedImpact[cap]; ok {
			d.Capability = string(cap)
			m.Degraded = append(m.Degraded, d)
		}
	}

	return m
}

func detectOne(cfg Config, cap collector.Capability) Status {
	switch cap {
	case InitSystemd:
		if _, ok := cfg.anyExists(cfg.SystemdDirs); ok {
			return Status{Available: true, Detail: "/run/systemd/system present"}
		}
		return Status{Available: false, Reason: "no systemd runtime directory"}

	case LogsJournald:
		if _, ok := cfg.anyExists(cfg.JournaldDirs); ok {
			return Status{Available: true, Detail: "persistent"}
		}
		return Status{Available: false, Reason: "no journal directory found"}

	case LogsSyslogfile:
		if path, ok := cfg.anyExists(cfg.SyslogFiles); ok {
			return Status{Available: true, Detail: path}
		}
		return Status{Available: false, Reason: "no syslog file found"}

	case PkgApt:
		if cfg.exists(cfg.DpkgStatus) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "dpkg status file not found"}

	case PkgDnf:
		if cfg.exists(cfg.RPMDBDir) {
			return Status{Available: true, Detail: cfg.RPMDBDir}
		}
		return Status{Available: false, Reason: "rpm database not found"}

	case StorageSmart:
		if path, ok := cfg.anyExists(cfg.SmartctlPaths); ok {
			return Status{Available: true, Detail: path}
		}
		return Status{Available: false, Reason: "smartctl not installed"}

	case StorageMdraid:
		if devices, ok := mdstatDevices(cfg); ok {
			return Status{Available: true, Detail: strings.Join(devices, ", ")}
		}
		return Status{Available: false, Reason: "no active md arrays"}

	case StorageSnapraid:
		if cfg.exists(cfg.SnapraidConf) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "snapraid.conf not found"}

	case StorageMergerfs:
		if hasMountFSType(cfg, "fuse.mergerfs") {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "no mergerfs mount found"}

	case StorageUnraidArray:
		if cfg.exists(cfg.UnraidMdcmd) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "not running on Unraid"}

	case HwHwmon:
		if cfg.dirNonEmpty(cfg.HwmonDir) {
			return Status{Available: true, Detail: hwmonNames(cfg)}
		}
		return Status{Available: false, Reason: "no hwmon devices (likely virtualized)"}

	case HwEdac:
		if cfg.exists(cfg.EdacDir) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "no EDAC support"}

	case HwRasdaemon:
		if _, ok := cfg.anyExists(cfg.RasdaemonPaths); ok {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "not installed"}

	case HwPstore:
		if cfg.dirNonEmpty(cfg.PstoreDir) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "ramoops not configured"}

	case ContainerDocker:
		if cfg.exists(cfg.DockerSocket) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "docker socket not found"}

	case ContainerCgroupv2:
		if cfg.exists(cfg.CgroupV2Marker) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "cgroup v2 unified hierarchy not mounted"}

	case NetTailscale:
		if cfg.exists(cfg.TailscaleSocket) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "tailscaled not running"}

	case SecSelinux:
		if cfg.exists(cfg.SelinuxDir) {
			if mode := selinuxMode(cfg); mode != "" {
				return Status{Available: true, Detail: mode}
			}
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "no SELinux filesystem mounted"}

	case SecApparmor:
		if cfg.exists(cfg.ApparmorDir) {
			return Status{Available: true}
		}
		return Status{Available: false, Reason: "no AppArmor securityfs interface"}

	case PublicExposed:
		if cfg.PubliclyExposed {
			return Status{Available: true, Detail: "operator-declared"}
		}
		return Status{Available: false, Reason: "not operator-declared"}

	default:
		return Status{Available: false, Reason: "unknown capability"}
	}
}

func detectOS(cfg Config) OSInfo {
	info := OSInfo{
		Family: runtime.GOOS,
		Arch:   runtime.GOARCH,
	}

	if data, err := readFile(cfg.path(cfg.KernelPath)); err == nil {
		info.Kernel = strings.TrimSpace(string(data))
	}

	if data, err := readFile(cfg.path(cfg.OSReleasePath)); err == nil {
		info.Distro, info.Version = parseOSRelease(data)
	}

	return info
}

// readFile guards os.ReadFile against an empty path, which callers pass
// when the corresponding Config field was left unset.
func readFile(path string) ([]byte, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(path)
}

func parseOSRelease(data []byte) (distro, version string) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "ID="):
			distro = unquote(strings.TrimPrefix(line, "ID="))
		case strings.HasPrefix(line, "VERSION_ID="):
			version = unquote(strings.TrimPrefix(line, "VERSION_ID="))
		}
	}
	return distro, version
}

func unquote(s string) string {
	return strings.Trim(s, `"`)
}

// mdstatDevices returns the md device names listed as active in
// /proc/mdstat (e.g. "md0"), skipping the "Personalities" header line.
func mdstatDevices(cfg Config) ([]string, bool) {
	data, err := readFile(cfg.path(cfg.MdstatPath))
	if err != nil {
		return nil, false
	}

	var devices []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "md") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				devices = append(devices, fields[0])
			}
		}
	}
	return devices, len(devices) > 0
}

// hasMountFSType reports whether any line of /proc/mounts uses fsType as
// its filesystem type field.
func hasMountFSType(cfg Config, fsType string) bool {
	data, err := readFile(cfg.path(cfg.ProcMounts))
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[2] == fsType {
			return true
		}
	}
	return false
}

func hwmonNames(cfg Config) string {
	entries, err := os.ReadDir(cfg.path(cfg.HwmonDir))
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if nameData, err := readFile(filepath.Join(cfg.path(cfg.HwmonDir), e.Name(), "name")); err == nil {
			names = append(names, strings.TrimSpace(string(nameData)))
		}
	}
	return strings.Join(names, ", ")
}

func selinuxMode(cfg Config) string {
	data, err := readFile(cfg.path(cfg.SelinuxEnforce))
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(data)) {
	case "1":
		return "enforcing"
	case "0":
		return "permissive"
	default:
		return ""
	}
}
