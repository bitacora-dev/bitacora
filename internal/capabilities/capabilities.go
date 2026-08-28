// Package capabilities implements the ADR-0004 capability manifest: probing
// what a host can actually do (init system, log source, package manager,
// storage, hardware, containers, security modules) and building the
// declarative JSON manifest the agent sends to the hub on startup and on
// change.
//
// Detection never shells out (ADR-0012 restricts os/exec to helpers and
// bitacora-run): every check reads the filesystem directly, the same way
// internal/doctor does.
package capabilities

import (
	"time"

	"github.com/bitacora-dev/bitacora/internal/collector"
)

// Capability names, as they appear in the manifest (ADR-0004).
const (
	InitSystemd        collector.Capability = "init.systemd"
	LogsJournald       collector.Capability = "logs.journald"
	LogsSyslogfile     collector.Capability = "logs.syslogfile"
	PkgApt             collector.Capability = "pkg.apt"
	PkgDnf             collector.Capability = "pkg.dnf"
	StorageSmart       collector.Capability = "storage.smart"
	StorageMdraid      collector.Capability = "storage.mdraid"
	StorageSnapraid    collector.Capability = "storage.snapraid"
	StorageMergerfs    collector.Capability = "storage.mergerfs"
	StorageUnraidArray collector.Capability = "storage.unraid_array"
	HwHwmon            collector.Capability = "hw.hwmon"
	HwEdac             collector.Capability = "hw.edac"
	HwRasdaemon        collector.Capability = "hw.rasdaemon"
	HwPstore           collector.Capability = "hw.pstore"
	ContainerDocker    collector.Capability = "container.docker"
	ContainerCgroupv2  collector.Capability = "container.cgroupv2"
	NetTailscale       collector.Capability = "net.tailscale"
	SecSelinux         collector.Capability = "sec.selinux"
	SecApparmor        collector.Capability = "sec.apparmor"
	PublicExposed      collector.Capability = "public.exposed"
)

// All lists every capability the agent knows how to probe, in the order
// ADR-0004 documents them. Used to build a manifest with a stable key set.
var All = []collector.Capability{
	InitSystemd,
	LogsJournald,
	LogsSyslogfile,
	PkgApt,
	PkgDnf,
	StorageSmart,
	StorageMdraid,
	StorageSnapraid,
	StorageMergerfs,
	StorageUnraidArray,
	HwHwmon,
	HwEdac,
	HwRasdaemon,
	HwPstore,
	ContainerDocker,
	ContainerCgroupv2,
	NetTailscale,
	SecSelinux,
	SecApparmor,
	PublicExposed,
}

// Status is one entry in the manifest's "capabilities" object.
type Status struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Degraded describes a capability that is missing and has a known, visible
// impact — the manifest's "degraded" list (ADR-0004: make the gap visible
// instead of silently not measuring something).
type Degraded struct {
	Capability string `json:"capability"`
	Impact     string `json:"impact"`
	Remedy     string `json:"remedy,omitempty"`
}

// OSInfo identifies the operating system the agent runs on.
type OSInfo struct {
	Family  string `json:"family"`
	Distro  string `json:"distro,omitempty"`
	Version string `json:"version,omitempty"`
	Kernel  string `json:"kernel,omitempty"`
	Arch    string `json:"arch"`
}

// Manifest is the declarative document the agent sends to the hub on
// startup and whenever detection finds a change (ADR-0004).
type Manifest struct {
	HostID       string                          `json:"host_id"`
	Hostname     string                          `json:"hostname"`
	ReportedAt   time.Time                       `json:"reported_at"`
	AgentVersion string                          `json:"agent_version"`
	OS           OSInfo                          `json:"os"`
	Capabilities map[collector.Capability]Status `json:"capabilities"`
	Degraded     []Degraded                      `json:"degraded,omitempty"`
}

// Available reduces the manifest to the map Registry.Resolve expects.
func (m Manifest) Available() map[collector.Capability]bool {
	out := make(map[collector.Capability]bool, len(m.Capabilities))
	for name, status := range m.Capabilities {
		out[name] = status.Available
	}
	return out
}
